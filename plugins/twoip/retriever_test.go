package twoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

func newTestRetriever(t *testing.T, handler http.HandlerFunc) *retriever {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	r, err := newRetriever(Config{BaseURL: srv.URL}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestGetIPAddress(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr string
	}{
		{name: "success", status: 200, body: `{"ip":"203.0.113.7","city":"Berlin"}`, want: "203.0.113.7"},
		{name: "authorization error", status: 401, body: `{"error":"Unauthorized","message":"Undefined method. Get your free token at 2ip.io/free"}`, wantErr: "401"},
		{name: "error status", status: 429, body: `{"error":"Too Many Requests"}`, wantErr: "429"},
		{name: "not json", status: 200, body: "<html>oops</html>", wantErr: "not valid JSON"},
		{name: "empty body", status: 200, body: "", wantErr: "not valid JSON"},
		{name: "service error field", status: 200, body: `{"error":"something went wrong"}`, wantErr: "service error"},
		{name: "ip field not an address", status: 200, body: `{"ip":"not-an-ip"}`, wantErr: "not an IP address"},
		{name: "ipv6 address rejected", status: 200, body: `{"ip":"2001:db8::1"}`, wantErr: "not an IPv4 address"},
		{name: "loopback", status: 200, body: `{"ip":"127.0.0.1"}`, wantErr: "global unicast"},
		{name: "unspecified", status: 200, body: `{"ip":"0.0.0.0"}`, wantErr: "global unicast"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRetriever(t, reply(tt.status, tt.body))

			got, err := r.GetIPAddress(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != netip.MustParseAddr(tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestGetIPAddressRequest(t *testing.T) {
	var path, method, accept string

	r := newTestRetriever(t, func(w http.ResponseWriter, req *http.Request) {
		path, method, accept = req.URL.Path, req.Method, req.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"ip":"203.0.113.7"}`))
	})

	if _, err := r.GetIPAddress(context.Background()); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || path != "/" {
		t.Errorf("request = %s %s, want GET /", method, path)
	}
	if accept != "application/json" {
		t.Errorf("Accept header = %q, want application/json", accept)
	}
}

func TestGetIPAddressCancelled(t *testing.T) {
	release := make(chan struct{})
	r := newTestRetriever(t, func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.GetIPAddress(ctx)
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want prompt return on cancelled context", elapsed)
	}
}

func TestNewRetrieverValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "bad url", cfg: Config{BaseURL: "api.2ip.io"}, wantErr: "base_url"},
		{name: "bad url with a login", cfg: Config{BaseURL: "ftp://user:hunter2@api.2ip.io"}, wantErr: "base_url"},
		{name: "unparsable url with a password", cfg: Config{BaseURL: "https://user:hunter2@api.2ip.io/%zz"}, wantErr: "base_url"},
		{name: "valid", cfg: Config{BaseURL: "https://api.2ip.io"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newRetriever(tt.cfg, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error leaks the password from base_url: %v", err)
			}
		})
	}
}

func TestRegisteredInDefault(t *testing.T) {
	ret, err := plugin.Default.BuildRetriever(Name, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if ret == nil {
		t.Fatal("nil retriever")
	}

	if _, err := plugin.Default.BuildRetriever(Name, map[string]any{"bse_url": "https://api.2ip.io"}); err == nil {
		t.Error("misspelled parameter was accepted")
	}
}
