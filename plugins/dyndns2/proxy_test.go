package dyndns2

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/internal/socks5test"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	proxyUser = "proxyuser"
	proxyPass = "pr0xy-s3cret"

	// apiHost resolves nowhere: only the test proxy knows where it leads.
	apiHost = "api.dyndns.test"
)

func TestProviderUsesProxy(t *testing.T) {
	svc, srv := newFakeService(t, "good")

	proxy := socks5test.Start(t, proxyUser, proxyPass)
	proxy.Route(apiHost+":80", srv.Listener.Addr().String())

	cfg := testConfig(srv)
	cfg.BaseURL = "http://" + apiHost + "/nic/update"
	cfg.Proxy = "socks5://" + proxyUser + ":" + proxyPass + "@" + proxy.Addr

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := update(context.Background(), p, plugin.Addresses{V4: v4}); err != nil {
		t.Fatal(err)
	}

	if got := svc.query(t).Get("myip"); got != "203.0.113.7" {
		t.Errorf("myip = %q", got)
	}
	if got := proxy.Targets(); len(got) == 0 || got[0] != apiHost+":80" {
		t.Errorf("proxy targets = %v, want the API host name passed on for the proxy to resolve", got)
	}
}

func TestProxyUnreachable(t *testing.T) {
	svc, srv := newFakeService(t, "good")

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
	if strings.Contains(err.Error(), proxyPass) {
		t.Errorf("error leaks the proxy password: %v", err)
	}
	if len(svc.requests) != 0 {
		t.Error("the service was reached without the proxy")
	}
}

func TestProxyValidatedAtBuild(t *testing.T) {
	params := map[string]any{
		"base_url": "https://api.test/update", "username": "u", "password": "p", "hostname": "home.example.com",
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
