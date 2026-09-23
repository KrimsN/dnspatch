package regru

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/internal/socks5test"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	proxyUser = "proxyuser"
	proxyPass = "pr0xy-s3cret"

	// apiHost resolves nowhere: only the test proxy knows where it leads.
	apiHost = "api.regru.test"
)

// proxiedConfig points a provider at the fake API through a SOCKS5 proxy that
// requires a login.
func proxiedConfig(t *testing.T, srv *httptest.Server) (Config, *socks5test.Server) {
	t.Helper()

	proxy := socks5test.Start(t, proxyUser, proxyPass)
	proxy.Route(apiHost+":80", srv.Listener.Addr().String())

	cfg := testConfig(srv)
	cfg.BaseURL = "http://" + apiHost
	cfg.Proxy = "socks5://" + proxyUser + ":" + proxyPass + "@" + proxy.Addr

	return cfg, proxy
}

// realProvider builds a provider the way the registry does, with the client
// that newProvider chooses from cfg. mustProvider injects the test server's
// client instead, which would bypass the proxy.
func realProvider(t *testing.T, cfg Config) *provider {
	t.Helper()

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func TestProviderUsesProxy(t *testing.T) {
	api, srv := newFakeAPI(t)
	cfg, proxy := proxiedConfig(t, srv)

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := update(context.Background(), p, v4); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_alias" {
		t.Errorf("api calls = %s", got)
	}
	if got := proxy.Targets(); len(got) == 0 {
		t.Error("the request did not go through the proxy")
	} else if got[0] != apiHost+":80" {
		t.Errorf("proxy targets = %v, want the API host name passed on for the proxy to resolve", got)
	}
}

func TestProviderWithoutProxyConnectsDirectly(t *testing.T) {
	api, srv := newFakeAPI(t)

	// A proxy exists but the provider is not told about it.
	proxy := socks5test.Start(t, "", "")

	p, err := newProvider(testConfig(srv), nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := update(context.Background(), p, v4); err != nil {
		t.Fatal(err)
	}

	if len(api.methods()) == 0 {
		t.Error("the API was not reached")
	}
	if got := proxy.Targets(); len(got) != 0 {
		t.Errorf("proxy served %v, want no traffic", got)
	}
}

func TestProxyUnreachable(t *testing.T) {
	api, srv := newFakeAPI(t)
	cfg := testConfig(srv)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	for _, scheme := range []string{"socks5", "socks5h", "http"} {
		t.Run(scheme, func(t *testing.T) {
			cfg.Proxy = scheme + "://" + proxyUser + ":" + proxyPass + "@" + addr

			err := update(context.Background(), realProvider(t, cfg), v4)
			if err == nil || !strings.Contains(err.Error(), "proxyconnect") {
				t.Fatalf("error = %v, want it to name the proxy connection", err)
			}
			if strings.Contains(err.Error(), proxyPass) {
				t.Errorf("error leaks the proxy password: %v", err)
			}
		})
	}

	if got := api.methods(); len(got) != 0 {
		t.Errorf("api calls = %v, want none: the proxy never answered", got)
	}
}

func TestProxyRejectsCredentials(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg, _ := proxiedConfig(t, srv)
	cfg.Proxy = strings.Replace(cfg.Proxy, proxyPass, "wrong-pass", 1)

	err := update(context.Background(), realProvider(t, cfg), v4)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), proxyPass) || strings.Contains(err.Error(), "wrong-pass") {
		t.Errorf("error leaks the proxy password: %v", err)
	}
}

func TestUpdateCancelledThroughProxy(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	cfg, _ := proxiedConfig(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := update(ctx, realProvider(t, cfg), v4); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want prompt return on cancelled context", elapsed)
	}
}

func TestProxyValidatedAtConstruction(t *testing.T) {
	valid := Config{Username: "u", Password: "p", Zone: "example.com", RRName: "home", BaseURL: "https://api.test"}

	tests := []struct {
		name    string
		proxy   string
		wantErr string
	}{
		{name: "socks5", proxy: "socks5://203.0.113.5:1080"},
		{name: "socks5h", proxy: "socks5h://proxy.example.com:1080"},
		{name: "http", proxy: "http://proxy.example.com:3128"},
		{name: "https", proxy: "https://proxy.example.com:443"},
		{name: "unset"},
		{name: "unsupported scheme", proxy: "ftp://u:" + proxyPass + "@proxy.example.com", wantErr: "scheme"},
		{name: "no host", proxy: "socks5://u:" + proxyPass + "@", wantErr: "host"},
		{name: "garbage", proxy: "%zz" + proxyPass, wantErr: "not a valid URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			cfg.Proxy = tt.proxy

			_, err := newProvider(cfg, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), "proxy") || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want a proxy error containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), proxyPass) {
				t.Errorf("error leaks the proxy password: %v", err)
			}
		})
	}
}

func TestProxyParameterIsRegistered(t *testing.T) {
	params := map[string]any{
		"username": "u", "password": "p", "zone": "example.com", "rr_name": "@",
		"proxy": "socks5://203.0.113.5:1080",
	}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatalf("a valid proxy is rejected: %v", err)
	}

	params["proxy"] = "ftp://u:" + proxyPass + "@proxy.example.com"
	_, err := plugin.Default.BuildProvider(Name, params)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want the bad scheme reported at build time", err)
	}
	if strings.Contains(err.Error(), proxyPass) {
		t.Errorf("error leaks the proxy password: %v", err)
	}
}

func TestProxyParameterIsDocumented(t *testing.T) {
	// The field is promoted from the embedded proxy block.
	field, ok := reflect.TypeFor[Config]().FieldByName("Proxy")
	if !ok {
		t.Fatal("Config has no Proxy field")
	}

	doc := field.Tag.Get("doc")
	for _, want := range []string{"socks5", "socks5h", "https", "direct", "retriever"} {
		if !strings.Contains(doc, want) {
			t.Errorf("proxy doc does not mention %q: %s", want, doc)
		}
	}
}

func TestProxyDirectIgnoresEnvironment(t *testing.T) {
	cfg := Config{Username: "u", Password: "p", Zone: "example.com", RRName: "home", BaseURL: "https://api.test"}
	cfg.Proxy = "direct"

	p := realProvider(t, cfg)
	if p.client.Transport.(*http.Transport).Proxy != nil {
		t.Error("direct must leave the transport without a proxy function, so the environment is ignored")
	}

	cfg.Proxy = ""
	p = realProvider(t, cfg)
	if p.client.Transport.(*http.Transport).Proxy == nil {
		t.Error("an empty proxy must keep following the environment")
	}
}
