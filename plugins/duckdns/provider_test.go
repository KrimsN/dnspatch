package duckdns

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

const testToken = "duck-test-token"

var (
	v4 = netip.MustParseAddr("203.0.113.7")
	v6 = netip.MustParseAddr("2001:db8::7")
)

// fakeService records the requests it receives and answers with a fixed body.
type fakeService struct {
	mu       sync.Mutex
	requests []*http.Request
	body     string
	status   int
}

func newFakeService(t *testing.T, body string) (*fakeService, *httptest.Server) {
	t.Helper()

	svc := &fakeService{body: body, status: http.StatusOK}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		defer svc.mu.Unlock()

		svc.requests = append(svc.requests, r)

		if r.URL.Query().Get("token") != testToken {
			// The real service answers 200 with KO here.
			_, _ = fmt.Fprint(w, "KO")
			return
		}

		w.WriteHeader(svc.status)
		_, _ = fmt.Fprint(w, svc.body)
	}))
	t.Cleanup(srv.Close)

	return svc, srv
}

// query returns the query of the only request the service received.
func (s *fakeService) query(t *testing.T) url.Values {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(s.requests))
	}

	return s.requests[0].URL.Query()
}

func testConfig(srv *httptest.Server) Config {
	return Config{
		BaseURL: srv.URL + "/update",
		Domain:  "myhost",
		Token:   testToken,
	}
}

func mustProvider(t *testing.T, srv *httptest.Server) *provider {
	t.Helper()

	return providerFor(t, testConfig(srv), srv)
}

func providerFor(t *testing.T, cfg Config, srv *httptest.Server) *provider {
	t.Helper()

	p, err := newProvider(cfg, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func update(ctx context.Context, p plugin.Provider, addrs plugin.Addresses) error {
	return p.Update(ctx, addrs, plugin.RecordOptions{})
}

func TestUpdateSendsBothAddressesInOneRequest(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4, V6: v6}); err != nil {
		t.Fatal(err)
	}

	q := svc.query(t)
	if q.Get("domains") != "myhost" || q.Get("token") != testToken || q.Get("ip") != "203.0.113.7" || q.Get("ipv6") != "2001:db8::7" {
		t.Errorf("query = %v", q)
	}

	req := svc.requests[0]
	if req.Method != http.MethodGet || req.URL.Path != "/update" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
}

func TestUpdateLeavesUnreportedFamilyOut(t *testing.T) {
	tests := []struct {
		name  string
		addrs plugin.Addresses
		want  url.Values
	}{
		{
			name:  "v4 only",
			addrs: plugin.Addresses{V4: v4},
			want:  url.Values{"domains": {"myhost"}, "token": {testToken}, "ip": {"203.0.113.7"}},
		},
		{
			name:  "v6 only",
			addrs: plugin.Addresses{V6: v6},
			want:  url.Values{"domains": {"myhost"}, "token": {testToken}, "ipv6": {"2001:db8::7"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, srv := newFakeService(t, "OK")

			if err := update(context.Background(), mustProvider(t, srv), tt.addrs); err != nil {
				t.Fatal(err)
			}

			q := svc.query(t)
			if q.Has("ip") != tt.want.Has("ip") || q.Has("ipv6") != tt.want.Has("ipv6") {
				t.Errorf("query = %v, want %v (an unreported family must not appear, even empty)", q, tt.want)
			}
			if got := q.Encode(); got != tt.want.Encode() {
				t.Errorf("query = %s, want %s", got, tt.want.Encode())
			}
		})
	}
}

func TestUpdateNoAddress(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{}); err == nil {
		t.Fatal("expected an error")
	}
	if len(svc.requests) != 0 {
		t.Error("a request was sent without an address")
	}
}

func TestUpdateKeepsQueryOfBaseURL(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	cfg := testConfig(srv)
	cfg.BaseURL += "?verbose=true"

	if err := update(context.Background(), providerFor(t, cfg, srv), plugin.Addresses{V4: v4}); err != nil {
		t.Fatal(err)
	}

	q := svc.query(t)
	if q.Get("verbose") != "true" || q.Get("ip") != "203.0.113.7" {
		t.Errorf("query = %v", q)
	}
}

func TestUpdateReadsTheBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "ok", body: "OK"},
		{name: "ok with trailing newline", body: "OK\n"},
		{name: "ko", body: "KO", wantErr: "rejected"},
		{name: "unexpected body", body: "<html>maintenance</html>", wantErr: "did not confirm"},
		{name: "empty", body: "", wantErr: "did not confirm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, srv := newFakeService(t, tt.body)

			err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestUpdateWrongToken(t *testing.T) {
	_, srv := newFakeService(t, "OK")

	cfg := testConfig(srv)
	cfg.Token = "wrong-token"

	err := update(context.Background(), providerFor(t, cfg, srv), plugin.Addresses{V4: v4})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("error = %v, want the KO answer reported even though the status was 200", err)
	}
	if strings.Contains(err.Error(), "wrong-token") {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestUpdateHTTPErrorStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			svc, srv := newFakeService(t, "OK")
			svc.status = status

			if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestUpdateDoesNotFollowRedirects(t *testing.T) {
	var (
		mu     sync.Mutex
		leaked bool
	)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		leaked = true
	}))
	defer elsewhere.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4})
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("error = %v, want the redirect reported as a status", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if leaked {
		t.Error("the redirect was followed")
	}
}

func TestUpdateIgnoresRecordOptions(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	err := mustProvider(t, srv).Update(context.Background(), plugin.Addresses{V4: v4}, plugin.RecordOptions{TTL: 300})
	if err != nil {
		t.Fatal(err)
	}
	if q := svc.query(t); len(q) != 3 {
		t.Errorf("query = %v, want only domains, token and ip", q)
	}
}

func TestUpdateErrorDoesNotLeakToken(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cfg := Config{BaseURL: "http://" + addr + "/update", Domain: "myhost", Token: testToken}
	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = update(context.Background(), p, plugin.Addresses{V4: v4})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaks the token via the request URL: %v", err)
	}
}

func TestNewProviderValidatesConfig(t *testing.T) {
	valid := Config{BaseURL: "https://api.test/update", Domain: "myhost", Token: testToken}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "mixed case domain", mutate: func(c *Config) { c.Domain = "MyHost" }},
		{name: "bad url", mutate: func(c *Config) { c.BaseURL = "ftp://api.test" }, wantErr: "base_url"},
		{name: "no host", mutate: func(c *Config) { c.BaseURL = "https://" }, wantErr: "base_url"},
		{name: "empty domain", mutate: func(c *Config) { c.Domain = " " }, wantErr: "domain"},
		{name: "domain with dot suffix", mutate: func(c *Config) { c.Domain = "myhost.duckdns.org" }, wantErr: "domain"},
		{name: "several domains", mutate: func(c *Config) { c.Domain = "a,b" }, wantErr: "domain"},
		{name: "empty token", mutate: func(c *Config) { c.Token = " " }, wantErr: "token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			_, err := newProvider(cfg, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegisteredWithDefaults(t *testing.T) {
	params := map[string]any{"domain": "myhost", "token": testToken}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatalf("a minimal configuration is rejected: %v", err)
	}

	delete(params, "token")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil {
		t.Fatal("a configuration without token is accepted")
	}
}
