//go:build ping

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/internal/app"
)

// pingCounter counts the requests it receives, telling success pings
// (anything but "/fail") apart from Healthchecks.io-style failure pings.
type pingCounter struct {
	mu            sync.Mutex
	success, fail int
}

func (c *pingCounter) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.URL.Path == "/ping/fail" {
		c.fail++
	} else {
		c.success++
	}
}

func (c *pingCounter) counts() (success, fail int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.success, c.fail
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dnspatch.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// TestPingBuildPingsOnEveryCompletedCycle is the one end-to-end check
// that a build with the ping tag actually wires the ping hook into the daemon: unit
// coverage for the hook itself and for how the config field is threaded
// through lives in internal/hooks/ping and internal/app.
func TestPingBuildPingsOnEveryCompletedCycle(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(rw, "203.0.113.7")
	}))
	defer echo.Close()

	dns := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(rw, `{"result":"success","answer":{"domains":[{"dname":"example.com","result":"success","rrs":[]}]}}`)
	}))
	defer dns.Close()

	counter := &pingCounter{}
	pingSrv := httptest.NewServer(counter)
	defer pingSrv.Close()

	path := writeConfig(t, fmt.Sprintf(`
interval = "1s"

[retriever.echo]
type     = "ifconfigco"
base_url = %[1]q

[provider.dns]
type     = "regru"
username = "user"
password = "secret"
zone     = "example.com"
rr_name  = "home"
base_url = %[2]q

[[instance]]
name     = "home"
ping_url = %[3]q
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
`, echo.URL, dns.URL, pingSrv.URL+"/ping"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- app.Run(ctx, []string{"--config", path}, &bytes.Buffer{}, &bytes.Buffer{}, options())
	}()

	waitFor(t, "at least two successful pings", func() bool {
		success, _ := counter.counts()
		return success >= 2
	})

	cancel()
	select {
	case code := <-done:
		if code != app.ExitOK {
			t.Errorf("exit code = %d, want %d", code, app.ExitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}

	if _, fail := counter.counts(); fail != 0 {
		t.Errorf("fail pings = %d, want 0: every cycle succeeded", fail)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}
