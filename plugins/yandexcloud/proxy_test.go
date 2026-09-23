package yandexcloud

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/internal/socks5test"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	proxyUser = "proxyuser"
	proxyPass = "pr0xy-s3cret"

	// apiHost resolves nowhere: only the test proxy knows where it leads.
	apiHost = "api.yandexcloud.test"
)

// proxiedConfig points a provider at the fake API through a SOCKS5 proxy that
// requires a login.
func proxiedConfig(t *testing.T, srv *httptest.Server) (Config, *socks5test.Server) {
	t.Helper()

	proxy := socks5test.Start(t, proxyUser, proxyPass)
	proxy.Route(apiHost+":80", srv.Listener.Addr().String())

	cfg := testConfig(t, srv)
	cfg.IAMURL = "http://" + apiHost + "/iam/v1/tokens"
	cfg.BaseURL = "http://" + apiHost + "/dns/v1"
	cfg.Proxy = "socks5://" + proxyUser + ":" + proxyPass + "@" + proxy.Addr

	return cfg, proxy
}

func TestProviderUsesProxy(t *testing.T) {
	api, srv := newFakeAPI(t)
	cfg, proxy := proxiedConfig(t, srv)

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if _, ok := api.sets["home.example.com./A"]; !ok {
		t.Error("record was not written through the proxy")
	}
	if got := proxy.Targets(); len(got) == 0 || got[0] != apiHost+":80" {
		t.Errorf("proxy targets = %v, want the API host name passed on for the proxy to resolve", got)
	}
}

func TestProxyRejectsCredentials(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg, _ := proxiedConfig(t, srv)
	cfg.Proxy = strings.Replace(cfg.Proxy, proxyPass, "wrong-pass", 1)

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), proxyPass) || strings.Contains(err.Error(), "wrong-pass") {
		t.Errorf("error leaks the proxy password: %v", err)
	}
}
