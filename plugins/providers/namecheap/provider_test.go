package namecheap

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

	"github.com/dnspatch/dnspatch/plugin"
)

const testPassword = "namecheap-test-password"

var (
	v4 = netip.MustParseAddr("203.0.113.7")
	v6 = netip.MustParseAddr("2001:db8::7")
)

const okBody = `<?xml version="1.0"?>
<interface-response>
<ErrCount>0</ErrCount>
<errors></errors>
<ResponseCount>1</ResponseCount>
<Done>true</Done>
</interface-response>`

func errBody(msg string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<interface-response>
<ErrCount>1</ErrCount>
<errors><Err1>%s</Err1></errors>
<ResponseCount>1</ResponseCount>
<Done>true</Done>
</interface-response>`, msg)
}

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

		if r.URL.Query().Get("password") != testPassword {
			_, _ = fmt.Fprint(w, errBody("Invalid password"))
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
		BaseURL:  srv.URL + "/update",
		Host:     "@",
		Domain:   "example.com",
		Password: testPassword,
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

func TestUpdateWritesV4(t *testing.T) {
	svc, srv := newFakeService(t, okBody)

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4}); err != nil {
		t.Fatal(err)
	}

	q := svc.query(t)
	if q.Get("host") != "@" || q.Get("domain") != "example.com" || q.Get("password") != testPassword || q.Get("ip") != "203.0.113.7" {
		t.Errorf("query = %v", q)
	}

	req := svc.requests[0]
	if req.Method != http.MethodGet || req.URL.Path != "/update" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
}

func TestUpdateV6OnlyIsAnError(t *testing.T) {
	svc, srv := newFakeService(t, okBody)

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V6: v6}); err == nil {
		t.Fatal("expected an error")
	}
	if len(svc.requests) != 0 {
		t.Error("a request was sent for an unsupported IPv6-only update")
	}
}

func TestUpdateBothFamiliesWritesV4AndReportsV6(t *testing.T) {
	svc, srv := newFakeService(t, okBody)

	err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4, V6: v6})
	if err == nil || !strings.Contains(err.Error(), "AAAA") {
		t.Fatalf("error = %v, want it to mention AAAA is unsupported", err)
	}

	q := svc.query(t)
	if q.Get("ip") != "203.0.113.7" {
		t.Errorf("query = %v, want the V4 address written despite the reported error", q)
	}
}

func TestUpdateNoAddress(t *testing.T) {
	svc, srv := newFakeService(t, okBody)

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{}); err == nil {
		t.Fatal("expected an error")
	}
	if len(svc.requests) != 0 {
		t.Error("a request was sent without an address")
	}
}

func TestUpdateReadsTheBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "ok", body: okBody},
		{name: "error with reason", body: errBody("Domain name not found"), wantErr: "Domain name not found"},
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

func TestUpdateWrongPassword(t *testing.T) {
	_, srv := newFakeService(t, okBody)

	cfg := testConfig(srv)
	cfg.Password = "wrong-password"

	err := update(context.Background(), providerFor(t, cfg, srv), plugin.Addresses{V4: v4})
	if err == nil || !strings.Contains(err.Error(), "Invalid password") {
		t.Fatalf("error = %v, want the reported reason even though the status was 200", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestUpdateHTTPErrorStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			svc, srv := newFakeService(t, okBody)
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
	svc, srv := newFakeService(t, okBody)

	err := mustProvider(t, srv).Update(context.Background(), plugin.Addresses{V4: v4}, plugin.RecordOptions{TTL: 300})
	if err != nil {
		t.Fatal(err)
	}
	if q := svc.query(t); len(q) != 4 {
		t.Errorf("query = %v, want only host, domain, password and ip", q)
	}
}

func TestUpdateErrorDoesNotLeakPassword(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cfg := Config{BaseURL: "http://" + addr + "/update", Host: "@", Domain: "example.com", Password: testPassword}
	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = update(context.Background(), p, plugin.Addresses{V4: v4})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Errorf("error leaks the password via the request URL: %v", err)
	}
}

func TestNewProviderValidatesConfig(t *testing.T) {
	valid := Config{BaseURL: "https://api.test/update", Host: "@", Domain: "example.com", Password: testPassword}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "subdomain host", mutate: func(c *Config) { c.Host = "www" }},
		{name: "bad url", mutate: func(c *Config) { c.BaseURL = "ftp://api.test" }, wantErr: "base_url"},
		{name: "no host in url", mutate: func(c *Config) { c.BaseURL = "https://" }, wantErr: "base_url"},
		{name: "empty host", mutate: func(c *Config) { c.Host = " " }, wantErr: "host"},
		{name: "host with slash", mutate: func(c *Config) { c.Host = "a/b" }, wantErr: "host"},
		{name: "empty domain", mutate: func(c *Config) { c.Domain = " " }, wantErr: "domain"},
		{name: "domain with space", mutate: func(c *Config) { c.Domain = "example .com" }, wantErr: "domain"},
		{name: "empty password", mutate: func(c *Config) { c.Password = " " }, wantErr: "password"},
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
	params := map[string]any{"host": "@", "domain": "example.com", "password": testPassword}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatalf("a minimal configuration is rejected: %v", err)
	}

	delete(params, "password")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil {
		t.Fatal("a configuration without password is accepted")
	}
}
