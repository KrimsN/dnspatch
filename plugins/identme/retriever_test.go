package identme

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

// roundTripFunc lets a test build an http.Client whose RoundTrip is a plain
// function, to redirect a request to a specific test server regardless of
// the endpoint the retriever thinks it is calling.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// redirectTo builds a client that sends every request to target instead of
// the URL's own host, so two clients can each be pinned to a different fake
// server standing in for a different address family.
func redirectTo(t *testing.T, target string) *http.Client {
	t.Helper()

	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}

	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = u.Scheme
		clone.URL.Host = u.Host
		clone.Host = u.Host
		return http.DefaultTransport.RoundTrip(clone)
	})}
}

func newTestRetriever(t *testing.T, family string, handler http.HandlerFunc) *retriever {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	r, err := newRetriever(Config{BaseURL: srv.URL, Family: family}, srv.Client())
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

func TestGetAddresses(t *testing.T) {
	tests := []struct {
		name    string
		family  string
		status  int
		body    string
		want    string
		wantErr string
	}{
		{name: "ipv4", family: "ipv4", status: 200, body: "203.0.113.7", want: "203.0.113.7"},
		{name: "ipv6", family: "ipv6", status: 200, body: "2001:db8::1", want: "2001:db8::1"},
		{name: "mapped ipv4 is unwrapped", family: "ipv4", status: 200, body: "::ffff:203.0.113.7", want: "203.0.113.7"},
		{name: "error status", family: "ipv4", status: 429, body: "slow down", wantErr: "429"},
		{name: "html instead of address", family: "ipv4", status: 200, body: "<html>oops</html>", wantErr: "not an IP address"},
		{name: "empty body", family: "ipv4", status: 200, body: "", wantErr: "not an IP address"},
		{name: "wrong family", family: "ipv4", status: 200, body: "2001:db8::1", wantErr: "not an ipv4 address"},
		{name: "wrong family v6", family: "ipv6", status: 200, body: "203.0.113.7", wantErr: "not an ipv6 address"},
		{name: "loopback", family: "ipv4", status: 200, body: "127.0.0.1", wantErr: "global unicast"},
		{name: "unspecified", family: "ipv4", status: 200, body: "0.0.0.0", wantErr: "global unicast"},
		{name: "zone", family: "ipv6", status: 200, body: "fe80::1%eth0", wantErr: "global unicast"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRetriever(t, tt.family, reply(tt.status, tt.body))

			got, err := r.GetAddresses(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			want := netip.MustParseAddr(tt.want)
			var wantAddrs plugin.Addresses
			if want.Is4() {
				wantAddrs.V4 = want
			} else {
				wantAddrs.V6 = want
			}
			if got != wantAddrs {
				t.Errorf("got %+v, want %+v", got, wantAddrs)
			}
		})
	}
}

// dualRetriever builds a family="dual" retriever whose v4Client and v6Client
// each redirect to their own fake server, so the two legs can be told apart.
func dualRetriever(t *testing.T, v4, v6 http.HandlerFunc) *retriever {
	t.Helper()

	v4Srv := httptest.NewServer(v4)
	t.Cleanup(v4Srv.Close)
	v6Srv := httptest.NewServer(v6)
	t.Cleanup(v6Srv.Close)

	return &retriever{
		endpoint: v4Srv.URL + "/",
		family:   familyDual,
		v4Client: redirectTo(t, v4Srv.URL),
		v6Client: redirectTo(t, v6Srv.URL),
	}
}

func TestGetAddressesDual(t *testing.T) {
	r := dualRetriever(t, reply(200, "203.0.113.7"), reply(200, "2001:db8::1"))

	got, err := r.GetAddresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.7"), V6: netip.MustParseAddr("2001:db8::1")}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetAddressesDualFailsIfEitherLegFails(t *testing.T) {
	r := dualRetriever(t, reply(200, "203.0.113.7"), reply(500, "boom"))

	if _, err := r.GetAddresses(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestGetAddressesRequest(t *testing.T) {
	var path, method string

	r := newTestRetriever(t, "ipv4", func(w http.ResponseWriter, req *http.Request) {
		path, method = req.URL.Path, req.Method
		_, _ = w.Write([]byte("203.0.113.7"))
	})

	if _, err := r.GetAddresses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || path != "/" {
		t.Errorf("request = %s %s, want GET /", method, path)
	}
}

func TestGetAddressesCancelled(t *testing.T) {
	release := make(chan struct{})
	r := newTestRetriever(t, "ipv4", func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.GetAddresses(ctx)
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
		{name: "bad family", cfg: Config{BaseURL: "https://ident.me", Family: "carrier-pigeon"}, wantErr: "family"},
		{name: "bad url", cfg: Config{BaseURL: "ident.me", Family: "ipv4"}, wantErr: "base_url"},
		{name: "bad url with a login", cfg: Config{BaseURL: "ftp://user:hunter2@ident.me", Family: "ipv4"}, wantErr: "base_url"},
		{name: "unparsable url with a password", cfg: Config{BaseURL: "https://user:hunter2@ident.me/%zz", Family: "ipv4"}, wantErr: "base_url"},
		{name: "family is case-insensitive", cfg: Config{BaseURL: "https://ident.me", Family: "IPv6"}},
		{name: "dual is accepted", cfg: Config{BaseURL: "https://ident.me", Family: "dual"}},
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

func TestFamilyTransportDialsPinnedNetwork(t *testing.T) {
	// httptest listens on 127.0.0.1, which only an IPv4 dialer can reach.
	srv := httptest.NewServer(reply(200, "203.0.113.7"))
	defer srv.Close()

	for family, wantReachable := range map[string]bool{familyIPv4: true, familyIPv6: false} {
		client := &http.Client{Transport: familyTransport(family), Timeout: 2 * time.Second}

		resp, err := client.Get(srv.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if reachable := err == nil; reachable != wantReachable {
			t.Errorf("%s: reachable = %v (err %v), want %v", family, reachable, err, wantReachable)
		}
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

	if _, err := plugin.Default.BuildRetriever(Name, map[string]any{"famly": "ipv4"}); err == nil {
		t.Error("misspelled parameter was accepted")
	}
}
