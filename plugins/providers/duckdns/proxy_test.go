package duckdns

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/dnspatch/dnspatch/internal/socks5test"
	"github.com/dnspatch/dnspatch/plugin"
)

const (
	proxyUser = "proxyuser"
	proxyPass = "pr0xy-s3cret"

	// apiHost resolves nowhere: only the test proxy knows where it leads.
	apiHost = "www.duckdns.test"
)

func TestProviderUsesProxy(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	proxy := socks5test.Start(t, proxyUser, proxyPass)
	proxy.Route(apiHost+":80", srv.Listener.Addr().String())

	cfg := testConfig(srv)
	cfg.BaseURL = "http://" + apiHost + "/update"
	cfg.Proxy = "socks5://" + proxyUser + ":" + proxyPass + "@" + proxy.Addr

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := update(context.Background(), p, plugin.Addresses{V4: v4}); err != nil {
		t.Fatal(err)
	}

	if got := svc.query(t).Get("ip"); got != "203.0.113.7" {
		t.Errorf("ip = %q", got)
	}
	if got := proxy.Targets(); len(got) == 0 || got[0] != apiHost+":80" {
		t.Errorf("proxy targets = %v, want the API host name passed on for the proxy to resolve", got)
	}
}

func TestProviderWithoutProxyConnectsDirectly(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	// A proxy exists but the provider is not told about it.
	proxy := socks5test.Start(t, "", "")

	p, err := newProvider(testConfig(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := update(context.Background(), p, plugin.Addresses{V4: v4}); err != nil {
		t.Fatal(err)
	}

	if len(svc.requests) == 0 {
		t.Error("the service was not reached")
	}
	if got := proxy.Targets(); len(got) != 0 {
		t.Errorf("proxy served %v, want no traffic", got)
	}
}

func TestProxyUnreachable(t *testing.T) {
	svc, srv := newFakeService(t, "OK")

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cfg := testConfig(srv)
	cfg.Proxy = "socks5://" + proxyUser + ":" + proxyPass + "@" + addr

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = update(context.Background(), p, plugin.Addresses{V4: v4})
	if err == nil || !strings.Contains(err.Error(), "proxyconnect") {
		t.Fatalf("error = %v, want it to name the proxy connection", err)
	}
	if strings.Contains(err.Error(), proxyPass) || strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaks a secret: %v", err)
	}
	if len(svc.requests) != 0 {
		t.Error("the service was reached without the proxy")
	}
}

func TestProxyRejectsCredentials(t *testing.T) {
	_, srv := newFakeService(t, "OK")

	proxy := socks5test.Start(t, proxyUser, proxyPass)
	proxy.Route(apiHost+":80", srv.Listener.Addr().String())

	cfg := testConfig(srv)
	cfg.BaseURL = "http://" + apiHost + "/update"
	cfg.Proxy = "socks5://" + proxyUser + ":wrong-pass@" + proxy.Addr

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = update(context.Background(), p, plugin.Addresses{V4: v4})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), proxyPass) || strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaks a secret: %v", err)
	}
}

func TestProxyValidatedAtBuild(t *testing.T) {
	params := map[string]any{
		"domain": "myhost", "token": testToken,
		"proxy": "ftp://u:" + proxyPass + "@proxy.example.com",
	}

	_, err := plugin.Default.BuildProvider(Name, params)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want the bad scheme reported at build time", err)
	}
	if strings.Contains(err.Error(), proxyPass) {
		t.Errorf("error leaks the proxy password: %v", err)
	}

	params["proxy"] = "socks5://203.0.113.5:1080"
	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatalf("a valid proxy is rejected: %v", err)
	}
}

func TestProxyDirectIgnoresEnvironment(t *testing.T) {
	cfg := Config{BaseURL: "https://api.test/update", Domain: "myhost", Token: testToken}
	cfg.Proxy = "direct"

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.client.Transport.(*http.Transport).Proxy != nil {
		t.Error("direct must leave the transport without a proxy function, so the environment is ignored")
	}

	cfg.Proxy = ""
	p, err = newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.client.Transport.(*http.Transport).Proxy == nil {
		t.Error("an empty proxy must keep following the environment")
	}
}
