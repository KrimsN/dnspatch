package icanhazip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/internal/httpx"
	"github.com/KrimsN/dnspatch/internal/socks5test"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	proxyPass = "pr0xy-s3cret"

	// serviceHost resolves nowhere: only the test proxy knows where it leads.
	serviceHost = "icanhazip.test"
)

func withProxy(cfg Config, proxy string) Config {
	cfg.DirectProxyConfig = httpx.DirectProxyConfig{Proxy: proxy}

	return cfg
}

func transportOf(t *testing.T, r *retriever) *http.Transport {
	t.Helper()

	transport, ok := r.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", r.client.Transport)
	}

	return transport
}

// A retriever that went through a proxy would report the address of the proxy
// and the daemon would publish it in DNS, so by default the environment must
// not matter.
func TestRetrieverIgnoresProxyEnvironmentByDefault(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://proxy.example.com:3128")
	}

	base := Config{BaseURL: "https://icanhazip.com", Family: "ipv4"}

	// An empty value is treated like the default, not like a request for the
	// environment that a provider gets.
	for _, proxy := range []string{"", "direct"} {
		r, err := newRetriever(withProxy(base, proxy), nil)
		if err != nil {
			t.Fatal(err)
		}

		// http.ProxyFromEnvironment reads the variables once per process, so
		// checking the outcome for a request would depend on test order; a
		// nil function is the only state that never consults them.
		if transportOf(t, r).Proxy != nil {
			t.Errorf("proxy %q: the transport has a proxy function; it must connect directly", proxy)
		}
	}
}

func TestProxyDefaultsToDirect(t *testing.T) {
	cfg, err := plugin.Decode[Config](map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Proxy != httpx.Direct {
		t.Errorf("default proxy = %q, want %q", cfg.Proxy, httpx.Direct)
	}
}

func TestRetrieverThroughProxy(t *testing.T) {
	tests := []struct {
		name   string
		family string
		reply  string
		want   string
	}{
		{name: "ipv4", family: "ipv4", reply: "203.0.113.7\n", want: "203.0.113.7"},
		// The connection to the proxy is IPv4 here; only the reply says v6.
		{name: "ipv6 is not pinned on the connection to the proxy", family: "ipv6", reply: "2001:db8::1\n", want: "2001:db8::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(reply(200, tt.reply))
			defer srv.Close()

			proxy := socks5test.Start(t, "proxyuser", proxyPass)
			proxy.Route(serviceHost+":80", srv.Listener.Addr().String())

			cfg := withProxy(Config{BaseURL: "http://" + serviceHost, Family: tt.family},
				"socks5://proxyuser:"+proxyPass+"@"+proxy.Addr)

			r, err := newRetriever(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}

			got, err := r.GetIPAddress(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != netip.MustParseAddr(tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
			if targets := proxy.Targets(); len(targets) != 1 || targets[0] != serviceHost+":80" {
				t.Errorf("proxy targets = %v", targets)
			}
		})
	}
}

func TestRetrieverProxyStillChecksFamily(t *testing.T) {
	srv := httptest.NewServer(reply(200, "2001:db8::1\n"))
	defer srv.Close()

	proxy := socks5test.Start(t, "", "")
	proxy.Route(serviceHost+":80", srv.Listener.Addr().String())

	r, err := newRetriever(withProxy(Config{BaseURL: "http://" + serviceHost, Family: "ipv4"}, "socks5://"+proxy.Addr), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetIPAddress(context.Background()); err == nil || !strings.Contains(err.Error(), "not an ipv4 address") {
		t.Errorf("error = %v, want the wrong family reported", err)
	}
}

func TestRetrieverWithoutProxyIgnoresRunningOne(t *testing.T) {
	srv := httptest.NewServer(reply(200, "203.0.113.7\n"))
	defer srv.Close()

	proxy := socks5test.Start(t, "", "")

	r, err := newRetriever(Config{BaseURL: srv.URL, Family: "ipv4"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetIPAddress(context.Background()); err != nil {
		t.Fatal(err)
	}
	if targets := proxy.Targets(); len(targets) != 0 {
		t.Errorf("proxy served %v, want no traffic", targets)
	}
}

func TestRetrieverBadProxyFailsAtConstruction(t *testing.T) {
	cfg := withProxy(Config{BaseURL: "https://icanhazip.com", Family: "ipv4"}, "ftp://u:"+proxyPass+"@proxy.example.com")

	_, err := newRetriever(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "proxy") || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want a proxy scheme error", err)
	}
	if strings.Contains(err.Error(), proxyPass) {
		t.Errorf("error leaks the proxy password: %v", err)
	}
}
