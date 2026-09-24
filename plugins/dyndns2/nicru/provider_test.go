package nicru

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

func TestBuildSendsBothAddressesToTheService(t *testing.T) {
	var got *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = fmt.Fprint(w, "good")
	}))
	defer srv.Close()

	p, err := build(Config{Username: "u", Password: "p", Hostname: "home.example.com"}, srv.URL+"/dyndns/update")
	if err != nil {
		t.Fatal(err)
	}

	addrs := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.7"), V6: netip.MustParseAddr("2001:db8::7")}
	if err := p.Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if got == nil || got.URL.Path != "/dyndns/update" {
		t.Fatalf("request = %v", got)
	}
	if q := got.URL.Query(); q.Get("hostname") != "home.example.com" || q.Get("myip") != "203.0.113.7" || q.Get("ipv6") != "2001:db8::7" {
		t.Errorf("query = %v", q)
	}
	if user, pass, ok := got.BasicAuth(); !ok || user != "u" || pass != "p" {
		t.Errorf("basic auth = %q %q %v", user, pass, ok)
	}
	if got.Header.Get("User-Agent") == "" {
		t.Error("no User-Agent")
	}
}

func TestBuildReadsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "badauth")
	}))
	defer srv.Close()

	p, err := build(Config{Username: "u", Password: "p", Hostname: "home.example.com"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	err = p.Update(context.Background(), plugin.Addresses{V4: netip.MustParseAddr("203.0.113.7")}, plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "badauth") {
		t.Fatalf("error = %v, want badauth", err)
	}
}

func TestRegistered(t *testing.T) {
	params := map[string]any{"username": "u", "password": "p", "hostname": "home.example.com"}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatalf("a minimal configuration is rejected: %v", err)
	}

	params["base_url"] = "https://elsewhere.test"
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil {
		t.Error("base_url is accepted, but the URL is fixed; dyndns2 is the type for a different one")
	}

	params["proxy"] = "socks5://203.0.113.5:1080"
	delete(params, "base_url")
	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Errorf("a valid proxy is rejected: %v", err)
	}
}

func TestEndpointIsNICRU(t *testing.T) {
	if endpoint != "https://api.nic.ru/dyndns/update" {
		t.Errorf("endpoint = %s", endpoint)
	}
	if _, ok := reflect.TypeFor[Config]().FieldByName("BaseURL"); ok {
		t.Error("Config must not expose the update URL")
	}
}
