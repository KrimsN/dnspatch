package ping

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// pingServer records every path it was asked for.
type pingServer struct {
	mu    sync.Mutex
	paths []string
}

func (s *pingServer) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	s.mu.Unlock()
}

func (s *pingServer) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func TestBuildHooksReturnsNoneWithoutPingURL(t *testing.T) {
	hooks, err := BuildHooks(config.Instance{Name: "a"}, discardLogger())
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}
	if hooks != nil {
		t.Errorf("hooks = %v, want none", hooks)
	}
}

func TestBuildHooksRejectsAnInvalidURL(t *testing.T) {
	tests := []string{"not-a-url", "ftp://example.com/x", "https://"}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := BuildHooks(config.Instance{Name: "a", PingURL: raw}, discardLogger()); err == nil {
				t.Errorf("BuildHooks(%q) = nil error, want one", raw)
			}
		})
	}
}

func TestAfterCyclePingsSuccessURLOnSuccess(t *testing.T) {
	srv := &pingServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	hooks, err := BuildHooks(config.Instance{Name: "a", PingURL: ts.URL + "/abc"}, discardLogger())
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}

	hooks[0].AfterCycle(context.Background(), runner.CycleEvent{Instance: "a", Success: true})

	if got := srv.all(); !equal(got, []string{"/abc"}) {
		t.Errorf("paths hit = %v, want [/abc]", got)
	}
}

func TestAfterCyclePingsFailURLOnFailure(t *testing.T) {
	srv := &pingServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	hooks, err := BuildHooks(config.Instance{Name: "a", PingURL: ts.URL + "/abc"}, discardLogger())
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}

	hooks[0].AfterCycle(context.Background(), runner.CycleEvent{Instance: "a", Success: false})

	if got := srv.all(); !equal(got, []string{"/abc/fail"}) {
		t.Errorf("paths hit = %v, want [/abc/fail]", got)
	}
}

func TestAfterCycleDoesNotPanicWhenTheEndpointIsUnreachable(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))

	hooks, err := BuildHooks(config.Instance{Name: "a", PingURL: "http://127.0.0.1:1/abc"}, log)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}

	hooks[0].AfterCycle(context.Background(), runner.CycleEvent{Instance: "a", Success: true})

	if out := buf.String(); !strings.Contains(out, "monitoring ping failed") {
		t.Errorf("log = %q, want it to report the failed ping", out)
	}
}

func TestAfterCycleLogDoesNotLeakTheURLsSecretToken(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))

	const secretToken = "super-secret-token"
	hooks, err := BuildHooks(config.Instance{Name: "a", PingURL: "http://127.0.0.1:1/" + secretToken}, log)
	if err != nil {
		t.Fatalf("BuildHooks: %v", err)
	}

	hooks[0].AfterCycle(context.Background(), runner.CycleEvent{Instance: "a", Success: true})

	if out := buf.String(); strings.Contains(out, secretToken) {
		t.Errorf("log leaks the ping URL's secret token: %q", out)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
