package dyndns2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	testUser = "user"
	testPass = "s3cret"
)

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

		if user, pass, ok := r.BasicAuth(); !ok || user != testUser || pass != testPass {
			// The real services answer 200 here.
			_, _ = fmt.Fprint(w, "badauth")
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
		BaseURL:   srv.URL + "/nic/update",
		Username:  testUser,
		Password:  testPass,
		Hostname:  "home.example.com",
		IPParam:   "myip",
		IPv6Param: "ipv6",
		UserAgent: "dnspatch-test",
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
	svc, srv := newFakeService(t, "good 203.0.113.7\ngood 2001:db8::7\n")

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{V4: v4, V6: v6}); err != nil {
		t.Fatal(err)
	}

	q := svc.query(t)
	if q.Get("hostname") != "home.example.com" || q.Get("myip") != "203.0.113.7" || q.Get("ipv6") != "2001:db8::7" {
		t.Errorf("query = %v", q)
	}

	req := svc.requests[0]
	if req.Method != http.MethodGet || req.URL.Path != "/nic/update" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
	if got := req.Header.Get("User-Agent"); got != "dnspatch-test" {
		t.Errorf("User-Agent = %q", got)
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
			want:  url.Values{"hostname": {"home.example.com"}, "myip": {"203.0.113.7"}},
		},
		{
			name:  "v6 only",
			addrs: plugin.Addresses{V6: v6},
			want:  url.Values{"hostname": {"home.example.com"}, "ipv6": {"2001:db8::7"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, srv := newFakeService(t, "good")

			if err := update(context.Background(), mustProvider(t, srv), tt.addrs); err != nil {
				t.Fatal(err)
			}

			if got := svc.query(t).Encode(); got != tt.want.Encode() {
				t.Errorf("query = %s, want %s", got, tt.want.Encode())
			}
		})
	}
}

func TestUpdateNoAddress(t *testing.T) {
	svc, srv := newFakeService(t, "good")

	if err := update(context.Background(), mustProvider(t, srv), plugin.Addresses{}); err == nil {
		t.Fatal("expected an error")
	}
	if len(svc.requests) != 0 {
		t.Error("a request was sent without an address")
	}
}

func TestUpdateKeepsQueryOfBaseURL(t *testing.T) {
	svc, srv := newFakeService(t, "good")

	cfg := testConfig(srv)
	cfg.BaseURL += "?system=dyndns&wildcard=NOCHG"

	if err := update(context.Background(), providerFor(t, cfg, srv), plugin.Addresses{V4: v4}); err != nil {
		t.Fatal(err)
	}

	q := svc.query(t)
	if q.Get("system") != "dyndns" || q.Get("wildcard") != "NOCHG" || q.Get("myip") != "203.0.113.7" {
		t.Errorf("query = %v", q)
	}
}

func TestUpdateCustomParameterNames(t *testing.T) {
	svc, srv := newFakeService(t, "good")

	cfg := testConfig(srv)
	cfg.IPParam, cfg.IPv6Param = "ip", "myipv6"

	if err := update(context.Background(), providerFor(t, cfg, srv), plugin.Addresses{V4: v4, V6: v6}); err != nil {
		t.Fatal(err)
	}

	q := svc.query(t)
	if q.Get("ip") != "203.0.113.7" || q.Get("myipv6") != "2001:db8::7" {
		t.Errorf("query = %v", q)
	}
}

func TestUpdateReadsTheBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "good", body: "good 203.0.113.7"},
		{name: "nochg", body: "nochg 203.0.113.7\n"},
		{name: "case and spacing", body: "  GOOD   203.0.113.7  "},
		{name: "one line per address", body: "good 203.0.113.7\nnochg 2001:db8::7"},
		{name: "second line fails", body: "good 203.0.113.7\nnohost", wantErr: "nohost"},
		{name: "badauth", body: "badauth", wantErr: "login or password"},
		{name: "nohost", body: "nohost", wantErr: "does not exist"},
		{name: "notfqdn", body: "notfqdn", wantErr: "fully qualified"},
		{name: "abuse", body: "abuse", wantErr: "blocked"},
		{name: "badagent", body: "badagent", wantErr: "refuses this client"},
		{name: "dnserr", body: "dnserr", wantErr: "try again later"},
		{name: "911", body: "911", wantErr: "try again later"},
		{name: "unknown word", body: "wat 1.2.3.4", wantErr: "did not confirm"},
		{name: "html page", body: "<html>maintenance</html>", wantErr: "did not confirm"},
		{name: "empty", body: " \n", wantErr: "empty body"},
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
	_, srv := newFakeService(t, "good")

	cfg := testConfig(srv)
	cfg.Password = "wrong-pass"

	err := update(context.Background(), providerFor(t, cfg, srv), plugin.Addresses{V4: v4})
	if err == nil || !strings.Contains(err.Error(), "badauth") {
		t.Fatalf("error = %v, want badauth even though the status was 200", err)
	}
	if strings.Contains(err.Error(), "wrong-pass") {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestUpdateHTTPErrorStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			svc, srv := newFakeService(t, "good")
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
	svc, srv := newFakeService(t, "good")

	err := mustProvider(t, srv).Update(context.Background(), plugin.Addresses{V4: v4}, plugin.RecordOptions{TTL: 300})
	if err != nil {
		t.Fatal(err)
	}
	if q := svc.query(t); len(q) != 2 {
		t.Errorf("query = %v, want only hostname and myip", q)
	}
}

func TestNewProviderValidatesConfig(t *testing.T) {
	valid := Config{BaseURL: "https://api.test/update", Username: "u", Password: "p", Hostname: "home.example.com", IPParam: "myip", IPv6Param: "ipv6"}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "trailing dot", mutate: func(c *Config) { c.Hostname = "Home.Example.com." }},
		{name: "bad url", mutate: func(c *Config) { c.BaseURL = "ftp://api.test" }, wantErr: "base_url"},
		{name: "no host", mutate: func(c *Config) { c.BaseURL = "https://" }, wantErr: "base_url"},
		{name: "empty hostname", mutate: func(c *Config) { c.Hostname = " " }, wantErr: "hostname"},
		{name: "several hostnames", mutate: func(c *Config) { c.Hostname = "a.example.com,b.example.com" }, wantErr: "hostname"},
		{name: "same parameter", mutate: func(c *Config) { c.IPv6Param = "myip" }, wantErr: "differ"},
		{name: "empty parameter", mutate: func(c *Config) { c.IPParam = "" }, wantErr: "ip_param"},
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
	params := map[string]any{
		"base_url": "https://api.nic.ru/dyndns/update", "username": "u", "password": "p", "hostname": "home.example.com",
	}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatalf("a minimal configuration is rejected: %v", err)
	}

	delete(params, "hostname")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil {
		t.Fatal("a configuration without hostname is accepted")
	}
}
