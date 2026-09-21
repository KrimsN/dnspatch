package regru

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	testUser = "user"
	testPass = "s3cret"
)

// call is one API request the fake received.
type call struct {
	method string
	input  map[string]any
}

// fakeAPI is an in-memory stand-in for the zone functions of REG.API 2.
type fakeAPI struct {
	mu    sync.Mutex
	rrs   []resourceRecord
	calls []call
	// failWith makes the named method answer with a domain-level error.
	failWith map[string]string
}

func newFakeAPI(t *testing.T, rrs ...resourceRecord) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{rrs: rrs, failWith: map[string]string{}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()

		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if r.FormValue("username") != testUser || r.FormValue("password") != testPass {
			_, _ = fmt.Fprint(w, `{"result":"error","error_code":"PASSWORD_AUTH_FAILED","error_text":"Username/password Incorrect"}`)
			return
		}
		if r.FormValue("input_format") != "json" {
			http.Error(w, "input_format must be json", http.StatusBadRequest)
			return
		}

		var input map[string]any
		if err := json.Unmarshal([]byte(r.FormValue("input_data")), &input); err != nil {
			http.Error(w, "bad input_data", http.StatusBadRequest)
			return
		}

		method := strings.TrimPrefix(r.URL.Path, "/")
		api.calls = append(api.calls, call{method: method, input: input})

		if text, ok := api.failWith[method]; ok {
			_, _ = fmt.Fprintf(w, `{"result":"success","answer":{"domains":[{"dname":"example.com","result":"error","error_code":"DOMAIN_ERROR","error_text":%q}]}}`, text)
			return
		}

		api.serve(w, method, input)
	}))
	t.Cleanup(srv.Close)

	return api, srv
}

func (a *fakeAPI) serve(w http.ResponseWriter, method string, input map[string]any) {
	domain := map[string]any{"dname": "example.com", "result": "success"}

	switch method {
	case "zone/get_resource_records":
		rrs := a.rrs
		if rrs == nil {
			rrs = []resourceRecord{}
		}
		domain["rrs"] = rrs
	case "zone/add_alias", "zone/add_aaaa":
		rectype := map[string]string{"zone/add_alias": "A", "zone/add_aaaa": "AAAA"}[method]
		a.rrs = append(a.rrs, resourceRecord{
			Subname: input["subdomain"].(string), Rectype: rectype, Content: input["ipaddr"].(string),
		})
	case "zone/remove_record":
		var kept []resourceRecord
		for _, rr := range a.rrs {
			if rr.Subname == input["subdomain"] && rr.Rectype == input["record_type"] && rr.Content == input["content"] {
				continue
			}
			kept = append(kept, rr)
		}
		a.rrs = kept
	default:
		http.NotFound(w, nil)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"result": "success",
		"answer": map[string]any{"domains": []any{domain}},
	})
}

func (a *fakeAPI) methods() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	names := make([]string, len(a.calls))
	for i, c := range a.calls {
		names[i] = c.method
	}

	return names
}

func testConfig(srv *httptest.Server) Config {
	return Config{Username: testUser, Password: testPass, Zone: "example.com", RRName: "home", BaseURL: srv.URL}
}

func mustProvider(t *testing.T, cfg Config, srv *httptest.Server) *provider {
	t.Helper()

	p, err := newProvider(cfg, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	return p
}

var (
	v4 = netip.MustParseAddr("203.0.113.7")
	v6 = netip.MustParseAddr("2001:db8::7")
)

func TestSetIPAddressAddsMissingRecord(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_alias" {
		t.Errorf("calls = %s", got)
	}

	added := api.calls[1].input
	if added["subdomain"] != "home" || added["ipaddr"] != "203.0.113.7" {
		t.Errorf("add_alias input = %v", added)
	}
}

func TestSetIPAddressReplacesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t,
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "www", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "@", Rectype: "A", Content: "198.51.100.1"},
	)

	if err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}

	// The new record goes in before the old one goes out.
	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_alias zone/remove_record" {
		t.Fatalf("calls = %s", got)
	}

	removed := api.calls[2].input
	if removed["subdomain"] != "home" || removed["record_type"] != "A" || removed["content"] != "198.51.100.1" {
		t.Errorf("remove_record input = %v", removed)
	}

	want := map[string]string{"home": "203.0.113.7", "www": "198.51.100.1", "@": "198.51.100.1"}
	if len(api.rrs) != len(want) {
		t.Fatalf("records = %+v", api.rrs)
	}
	for _, rr := range api.rrs {
		if want[rr.Subname] != rr.Content {
			t.Errorf("record %s = %s, want %s", rr.Subname, rr.Content, want[rr.Subname])
		}
	}
}

func TestSetIPAddressSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "203.0.113.7"})

	if err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records" {
		t.Errorf("calls = %s, want the listing only", got)
	}
}

func TestSetIPAddressIPv6UsesAAAA(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "203.0.113.7"})

	if err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v6); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_aaaa" {
		t.Fatalf("calls = %s", got)
	}
	if api.calls[1].input["ipaddr"] != "2001:db8::7" {
		t.Errorf("add_aaaa input = %v", api.calls[1].input)
	}
	if len(api.rrs) != 2 {
		t.Errorf("the A record must stay: %+v", api.rrs)
	}
}

func TestSetIPAddressMatchesNamesLeniently(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "HOME", Rectype: "A", Content: "198.51.100.1"})

	cfg := testConfig(srv)
	cfg.RRName = "Home"
	if err := mustProvider(t, cfg, srv).SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.methods(), " "); !strings.HasSuffix(got, "zone/remove_record") {
		t.Errorf("calls = %s, want the old record replaced", got)
	}
}

func TestSetIPAddressApexAndWildcard(t *testing.T) {
	for _, label := range []string{"@", "*"} {
		api, srv := newFakeAPI(t)

		cfg := testConfig(srv)
		cfg.RRName = label
		if err := mustProvider(t, cfg, srv).SetIPAddress(context.Background(), v4); err != nil {
			t.Fatal(err)
		}
		if api.calls[1].input["subdomain"] != label {
			t.Errorf("label %q: add_alias input = %v", label, api.calls[1].input)
		}
	}
}

func TestSetIPAddressRefusesAmbiguousRecords(t *testing.T) {
	api, srv := newFakeAPI(t,
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.2"},
	)

	err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
	if err == nil || !strings.Contains(err.Error(), "2 A records") {
		t.Fatalf("error = %v", err)
	}
	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records" {
		t.Errorf("calls = %s, want no writes", got)
	}
}

func TestSetIPAddressKeepsOldRecordWhenAddFails(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"})
	api.failWith["zone/add_alias"] = "record limit reached"

	err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
	if err == nil || !strings.Contains(err.Error(), "record limit reached") {
		t.Fatalf("error = %v", err)
	}
	if len(api.rrs) != 1 || api.rrs[0].Content != "198.51.100.1" {
		t.Errorf("records = %+v, want the old record untouched", api.rrs)
	}
}

func TestSetIPAddressErrors(t *testing.T) {
	t.Run("wrong password names the API error and hides the password", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		cfg := testConfig(srv)
		cfg.Password = "wrong-password"

		err := mustProvider(t, cfg, srv).SetIPAddress(context.Background(), v4)
		if err == nil || !strings.Contains(err.Error(), "PASSWORD_AUTH_FAILED") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "wrong-password") {
			t.Errorf("error leaks the password: %v", err)
		}
	})

	t.Run("http error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream down", http.StatusBadGateway)
		}))
		defer srv.Close()

		err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
		if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream down") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("html instead of json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "<html>maintenance</html>")
		}))
		defer srv.Close()

		err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
		if err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("domain not on reg.ru dns", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failWith["zone/get_resource_records"] = "domain is not served by REG.RU DNS"

		err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
		if err == nil || !strings.Contains(err.Error(), "not served by REG.RU DNS") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("invalid address", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		if err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), netip.Addr{}); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestSetIPAddressCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := mustProvider(t, testConfig(srv), srv).SetIPAddress(ctx, v4); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want prompt return on cancelled context", elapsed)
	}
}

func TestNewProviderValidation(t *testing.T) {
	valid := Config{Username: "u", Password: "p", Zone: "example.com", RRName: "home", BaseURL: "https://api.test"}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"empty zone", func(c *Config) { c.Zone = "" }, "zone"},
		{"zone with a slash", func(c *Config) { c.Zone = "example.com/x" }, "zone"},
		{"empty rr_name", func(c *Config) { c.RRName = " " }, "rr_name"},
		{"rr_name with a trailing dot", func(c *Config) { c.RRName = "home." }, "rr_name"},
		{"bad base_url", func(c *Config) { c.BaseURL = "api.test" }, "base_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			if _, err := newProvider(cfg, nil); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegisteredInDefault(t *testing.T) {
	params := map[string]any{"username": "u", "password": "p", "zone": "example.com", "rr_name": "@"}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatal(err)
	}

	delete(params, "password")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("error = %v, want a missing password complaint", err)
	}
}
