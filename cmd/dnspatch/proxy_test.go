package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/internal/socks5test"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	proxyPass = "pr0xy-s3cret"

	// apiHost resolves nowhere: only the test proxy knows where it leads.
	apiHost = "api.regru.test"
)

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// proxyConfig is a daemon configuration whose retriever talks to echo
// directly and whose provider talks to the DNS API at apiHost through
// proxyURL.
func proxyConfig(echo, proxyURL string) string {
	return fmt.Sprintf(`
interval = "1s"

[retriever.echo]
type     = "ifconfigco"
base_url = %q

[provider.dns]
type     = "regru"
username = "user"
password = "secret"
zone     = "example.com"
rr_name  = "home"
base_url = "http://%s"
proxy    = %q

[[instance]]
name = "home"
[instance.retriever]
ref = "echo"
[[instance.provider]]
ref = "dns"
`, echo, apiHost, proxyURL)
}

func TestDaemonSendsOnlyProviderThroughProxy(t *testing.T) {
	world := &fakeWorld{ip: "203.0.113.7"}
	srv := httptest.NewServer(world)
	defer srv.Close()

	proxy := socks5test.Start(t, "proxyuser", proxyPass)
	proxy.Route(apiHost+":80", srv.Listener.Addr().String())

	path := writeConfig(t, proxyConfig(srv.URL, "socks5://proxyuser:"+proxyPass+"@"+proxy.Addr))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	log := &syncBuffer{buf: &stderr}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--config", path}, &bytes.Buffer{}, log, plugin.Default)
	}()

	waitFor(t, "address written through the proxy", func() bool {
		content, _ := world.snapshot()
		return content == "203.0.113.7"
	})

	// The address echo is on 127.0.0.1 and would show up here if the
	// retriever went through the proxy.
	for _, target := range proxy.Targets() {
		if target != apiHost+":80" {
			t.Errorf("proxy was asked for %s; only the DNS API may go through it", target)
		}
	}

	cancel()
	if code := <-done; code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if strings.Contains(log.String(), proxyPass) {
		t.Errorf("log leaks the proxy password:\n%s", log.String())
	}
}

func TestDaemonSurvivesUnreachableProxy(t *testing.T) {
	world := &fakeWorld{ip: "203.0.113.7"}
	srv := httptest.NewServer(world)
	defer srv.Close()

	// A port that was just closed refuses connections.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	path := writeConfig(t, proxyConfig(srv.URL, "socks5://proxyuser:"+proxyPass+"@"+addr))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	log := &syncBuffer{buf: &stderr}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--config", path}, &bytes.Buffer{}, log, plugin.Default)
	}()

	waitFor(t, "failure to be reported", func() bool {
		return strings.Contains(log.String(), "update failed")
	})

	// The daemon stays up: it is still polling and backing off.
	select {
	case code := <-done:
		t.Fatalf("daemon exited with %d after a proxy failure:\n%s", code, log.String())
	case <-time.After(200 * time.Millisecond):
	}

	out := log.String()
	if !strings.Contains(out, "proxyconnect") || !strings.Contains(out, "retry_in=") {
		t.Errorf("log does not show a proxy failure with a backoff:\n%s", out)
	}
	if strings.Contains(out, proxyPass) {
		t.Errorf("log leaks the proxy password:\n%s", out)
	}
	if content, _ := world.snapshot(); content != "" {
		t.Errorf("record written to %q although the proxy never answered", content)
	}

	cancel()
	if code := <-done; code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
}

func TestBadProxyFailsAtStartup(t *testing.T) {
	var stderr bytes.Buffer

	cfg := proxyConfig("http://127.0.0.1:1", "ftp://proxyuser:"+proxyPass+"@proxy.example.com")
	code := run(context.Background(), []string{"--config", writeConfig(t, cfg)}, &bytes.Buffer{}, &stderr, plugin.Default)

	if code != exitConfig {
		t.Errorf("exit code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "proxy") || !strings.Contains(stderr.String(), `instance "home"`) {
		t.Errorf("stderr = %q, want a proxy error naming the instance", stderr.String())
	}
	if strings.Contains(stderr.String(), proxyPass) {
		t.Errorf("stderr leaks the proxy password: %q", stderr.String())
	}
}
