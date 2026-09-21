package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

// fakeWorld plays both external services: the IP echo and the DNS API. It
// counts the writes the daemon makes.
type fakeWorld struct {
	mu      sync.Mutex
	ip      string
	content string
	writes  int
}

func (w *fakeWorld) setIP(ip string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ip = ip
}

func (w *fakeWorld) snapshot() (content string, writes int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.content, w.writes
}

func (w *fakeWorld) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch {
	case r.URL.Path == "/ip":
		_, _ = fmt.Fprint(rw, w.ip)
	case r.URL.Path == "/example.com/records" && r.Method == http.MethodGet:
		if w.content == "" {
			_, _ = fmt.Fprint(rw, `[]`)
			return
		}
		_, _ = fmt.Fprintf(rw, `[{"id":1,"type":"A","name":"home.example.com","content":%q,"ttl":300}]`, w.content)
	case strings.HasPrefix(r.URL.Path, "/example.com/records"):
		var body struct{ Content string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.content = body.Content
		w.writes++
		_, _ = fmt.Fprint(rw, `{}`)
	default:
		http.NotFound(rw, r)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dnspatch.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestDaemonEndToEnd(t *testing.T) {
	world := &fakeWorld{ip: "203.0.113.7"}
	srv := httptest.NewServer(world)
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`
interval = "1s"

[retriever.echo]
type     = "ifconfigco"
base_url = %[1]q

[provider.dns]
type      = "selectel_v1"
api_token = "token"
zone      = "example.com"
rr_name   = "home"
ttl       = 300
base_url  = %[1]q

[[instance]]
name = "home"
[instance.retriever]
ref = "echo"
[[instance.provider]]
ref = "dns"
`, srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--config", path}, &bytes.Buffer{}, &syncBuffer{buf: &stderr}, plugin.Default)
	}()

	waitFor(t, "first address written", func() bool {
		content, _ := world.snapshot()
		return content == "203.0.113.7"
	})

	// An unchanged address must not cause further writes over several ticks.
	time.Sleep(2500 * time.Millisecond)
	if _, writes := world.snapshot(); writes != 1 {
		t.Errorf("writes after unchanged ticks = %d, want 1", writes)
	}

	// A new address is picked up on a later tick.
	world.setIP("203.0.113.99")
	waitFor(t, "changed address written", func() bool {
		content, _ := world.snapshot()
		return content == "203.0.113.99"
	})

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}
}

func TestBadConfigExitsWithConfigCode(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "unknown provider type",
			config: `
[retriever.echo]
type = "ifconfigco"
[provider.dns]
type = "nosuch"
[[instance]]
name = "x"
[instance.retriever]
ref = "echo"
[[instance.provider]]
ref = "dns"
`,
			wantErr: `unknown provider type "nosuch"`,
		},
		{
			name: "missing required parameter",
			config: `
[retriever.echo]
type = "ifconfigco"
[provider.dns]
type = "selectel_v1"
zone = "example.com"
rr_name = "home"
[[instance]]
name = "x"
[instance.retriever]
ref = "echo"
[[instance.provider]]
ref = "dns"
`,
			wantErr: "api_token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			code := run(context.Background(), []string{"--config", writeConfig(t, tt.config)}, &bytes.Buffer{}, &stderr, plugin.Default)
			if code != exitConfig {
				t.Errorf("exit code = %d, want %d", code, exitConfig)
			}
			if !strings.Contains(stderr.String(), tt.wantErr) || !strings.Contains(stderr.String(), `instance "x"`) {
				t.Errorf("stderr = %q, want it to name the instance and contain %q", stderr.String(), tt.wantErr)
			}
		})
	}
}

func TestMissingConfigFile(t *testing.T) {
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "absent.toml")}, &bytes.Buffer{}, &stderr, plugin.Default)
	if code != exitConfig {
		t.Errorf("exit code = %d, want %d", code, exitConfig)
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout bytes.Buffer

	if code := run(context.Background(), []string{"--version"}, &stdout, &bytes.Buffer{}, plugin.Default); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout.String(), "dnspatch ") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	if code := run(context.Background(), []string{"--nope"}, &bytes.Buffer{}, &bytes.Buffer{}, plugin.Default); code != exitConfig {
		t.Errorf("exit code = %d, want %d", code, exitConfig)
	}
}

// syncBuffer lets the daemon's logger and the test share a buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf *bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
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
