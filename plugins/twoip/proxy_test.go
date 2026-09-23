package twoip

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
	serviceHost = "twoip.test"
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

// A retriever that went through a proxy would report the address of the
// proxy and the daemon would publish it in DNS, so by default the
// environment must not matter.
func TestRetrieverIgnoresProxyEnvironmentByDefault(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://proxy.example.com:3128")
	}

	base := Config{BaseURL: "https://api.2ip.io"}

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
	srv := httptest.NewServer(reply(200, `{"ip":"203.0.113.7"}`))
	defer srv.Close()

	proxy := socks5test.Start(t, "proxyuser", proxyPass)
	proxy.Route(serviceHost+":80", srv.Listener.Addr().String())

	cfg := withProxy(Config{BaseURL: "http://" + serviceHost},
		"socks5://proxyuser:"+proxyPass+"@"+proxy.Addr)

	r, err := newRetriever(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.getIPAddress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != netip.MustParseAddr("203.0.113.7") {
		t.Errorf("got %s, want 203.0.113.7", got)
	}
	if targets := proxy.Targets(); len(targets) != 1 || targets[0] != serviceHost+":80" {
		t.Errorf("proxy targets = %v", targets)
	}
}

func TestRetrieverWithoutProxyIgnoresRunningOne(t *testing.T) {
	srv := httptest.NewServer(reply(200, `{"ip":"203.0.113.7"}`))
	defer srv.Close()

	proxy := socks5test.Start(t, "", "")

	r, err := newRetriever(Config{BaseURL: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.getIPAddress(context.Background()); err != nil {
		t.Fatal(err)
	}
	if targets := proxy.Targets(); len(targets) != 0 {
		t.Errorf("proxy served %v, want no traffic", targets)
	}
}

func TestRetrieverBadProxyFailsAtConstruction(t *testing.T) {
	cfg := withProxy(Config{BaseURL: "https://api.2ip.io"}, "ftp://u:"+proxyPass+"@proxy.example.com")

	_, err := newRetriever(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "proxy") || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want a proxy scheme error", err)
	}
	if strings.Contains(err.Error(), proxyPass) {
		t.Errorf("error leaks the proxy password: %v", err)
	}
}
