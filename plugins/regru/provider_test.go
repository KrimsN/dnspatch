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
	"sync/atomic"
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

// update calls p.Update with addr in the family it belongs to, so the tests
// below can keep exercising one address at a time as they did before Update
// took both families at once.
func update(ctx context.Context, p *provider, addr netip.Addr) error {
	addrs := plugin.Addresses{}
	if addr.Is4() {
		addrs.V4 = addr
	} else {
		addrs.V6 = addr
	}
	return p.Update(ctx, addrs, plugin.RecordOptions{})
}

func TestUpdateAddsMissingRecord(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
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

func TestUpdateReplacesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t,
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "www", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "@", Rectype: "A", Content: "198.51.100.1"},
	)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
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

func TestUpdateSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "203.0.113.7"})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records" {
		t.Errorf("calls = %s, want the listing only", got)
	}
}

func TestUpdateIPv6UsesAAAA(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "203.0.113.7"})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
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

func TestUpdateWritesBothFamiliesInOneCall(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4, V6: v6}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_alias zone/get_resource_records zone/add_aaaa" {
		t.Fatalf("calls = %s", got)
	}
}

func TestUpdateWithOneInvalidFamilyWritesOnlyTheOther(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_alias" {
		t.Fatalf("calls = %s", got)
	}
}

func TestUpdateWithNoValidAddressFails(t *testing.T) {
	_, srv := newFakeAPI(t)

	err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), plugin.Addresses{}, plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "no address to write") {
		t.Fatalf("error = %v", err)
	}
}

func TestUpdateComparesAddressesNotText(t *testing.T) {
	forms := map[string]string{
		"expanded":   "2001:0db8:0000:0000:0000:0000:0000:0007",
		"upper case": "2001:DB8::7",
		"padded":     " 2001:db8::7 ",
	}

	for name, content := range forms {
		t.Run(name, func(t *testing.T) {
			api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "AAAA", Content: content})

			if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records" {
				t.Errorf("calls = %s, want the listing only", got)
			}
		})
	}
}

func TestUpdateRemovesStaleIPv6RecordAsTheAPIWroteIt(t *testing.T) {
	const stale = "2001:0db8:0000:0000:0000:0000:0000:0001"
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "AAAA", Content: stale})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/add_aaaa zone/remove_record" {
		t.Fatalf("calls = %s", got)
	}
	if got := api.calls[2].input["content"]; got != stale {
		t.Errorf("remove_record content = %v, want %q exactly as listed", got, stale)
	}
}

func TestSameAddress(t *testing.T) {
	tests := []struct {
		content string
		addr    netip.Addr
		want    bool
	}{
		{"203.0.113.7", v4, true},
		{"203.0.113.8", v4, false},
		{"::ffff:203.0.113.7", v4, true},
		{"2001:db8::7", v6, true},
		{"2001:db8:0:0:0:0:0:7", v6, true},
		{"2001:db8::8", v6, false},
		{"203.0.113.7", v6, false},
		{"not an address", v4, false},
		{"", v4, false},
	}

	for _, tt := range tests {
		if got := sameAddress(tt.content, tt.addr); got != tt.want {
			t.Errorf("sameAddress(%q, %s) = %t, want %t", tt.content, tt.addr, got, tt.want)
		}
	}
}

func TestUpdateMatchesNamesLeniently(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "HOME", Rectype: "A", Content: "198.51.100.1"})

	cfg := testConfig(srv)
	cfg.RRName = "Home"
	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(api.methods(), " "); !strings.HasSuffix(got, "zone/remove_record") {
		t.Errorf("calls = %s, want the old record replaced", got)
	}
}

func TestUpdateApexAndWildcard(t *testing.T) {
	for _, label := range []string{"@", "*"} {
		api, srv := newFakeAPI(t)

		cfg := testConfig(srv)
		cfg.RRName = label
		if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
			t.Fatal(err)
		}
		if api.calls[1].input["subdomain"] != label {
			t.Errorf("label %q: add_alias input = %v", label, api.calls[1].input)
		}
	}
}

func TestUpdateRefusesAmbiguousRecords(t *testing.T) {
	api, srv := newFakeAPI(t,
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.2"},
	)

	err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
	if err == nil || !strings.Contains(err.Error(), "2 A records") || !strings.Contains(err.Error(), "none is 203.0.113.7") {
		t.Fatalf("error = %v", err)
	}
	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records" {
		t.Errorf("calls = %s, want no writes", got)
	}
}

func TestUpdateRemovesStaleRecordsNextToCurrentOne(t *testing.T) {
	api, srv := newFakeAPI(t,
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "home", Rectype: "A", Content: "203.0.113.7"},
		resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.2"},
		resourceRecord{Subname: "www", Rectype: "A", Content: "198.51.100.1"},
		resourceRecord{Subname: "home", Rectype: "AAAA", Content: "2001:db8::1"},
	)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(api.methods(), " "); got != "zone/get_resource_records zone/remove_record zone/remove_record" {
		t.Fatalf("calls = %s, want removals and no add", got)
	}

	want := map[string]string{"home/A": "203.0.113.7", "www/A": "198.51.100.1", "home/AAAA": "2001:db8::1"}
	if len(api.rrs) != len(want) {
		t.Fatalf("records = %+v", api.rrs)
	}
	for _, rr := range api.rrs {
		if key := rr.Subname + "/" + rr.Rectype; want[key] != rr.Content {
			t.Errorf("record %s = %s, want %s", key, rr.Content, want[key])
		}
	}
}

func TestUpdateHealsAfterFailedRemoval(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"})
	p := mustProvider(t, testConfig(srv), srv)

	// The add goes through and the removal fails: both records stay in the zone.
	api.failWith["zone/remove_record"] = "try again later"
	if err := update(context.Background(), p, v4); err == nil || !strings.Contains(err.Error(), "try again later") {
		t.Fatalf("first attempt: error = %v", err)
	}
	if len(api.rrs) != 2 {
		t.Fatalf("records after the failed replacement = %+v, want both", api.rrs)
	}

	// The next attempt must not be stuck on the two records.
	delete(api.failWith, "zone/remove_record")
	if err := update(context.Background(), p, v4); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if len(api.rrs) != 1 || api.rrs[0].Content != "203.0.113.7" {
		t.Errorf("records = %+v, want only the new one", api.rrs)
	}
}

func TestUpdateKeepsOldRecordWhenAddFails(t *testing.T) {
	api, srv := newFakeAPI(t, resourceRecord{Subname: "home", Rectype: "A", Content: "198.51.100.1"})
	api.failWith["zone/add_alias"] = "record limit reached"

	err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
	if err == nil || !strings.Contains(err.Error(), "record limit reached") {
		t.Fatalf("error = %v", err)
	}
	if len(api.rrs) != 1 || api.rrs[0].Content != "198.51.100.1" {
		t.Errorf("records = %+v, want the old record untouched", api.rrs)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Run("wrong password names the API error and hides the password", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		cfg := testConfig(srv)
		cfg.Password = "wrong-password"

		err := update(context.Background(), mustProvider(t, cfg, srv), v4)
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

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream down") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("html instead of json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "<html>maintenance</html>")
		}))
		defer srv.Close()

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("domain not on reg.ru dns", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failWith["zone/get_resource_records"] = "domain is not served by REG.RU DNS"

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "not served by REG.RU DNS") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("invalid address", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), netip.Addr{}); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestUpdateCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := update(ctx, mustProvider(t, testConfig(srv), srv), v4); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want prompt return on cancelled context", elapsed)
	}
}

func TestUpdateDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("password") != "" {
			leaked.Store(true)
		}
		http.Error(w, "should not be reached", http.StatusTeapot)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.RedirectHandler(target.URL+"/zone/get_resource_records", http.StatusTemporaryRedirect))
	defer origin.Close()

	err := update(context.Background(), mustProvider(t, testConfig(origin), origin), v4)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("error = %v, want the redirect reported as an unexpected status", err)
	}
	if leaked.Load() {
		t.Error("the password was sent to the redirect target")
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
		{"base_url with a login", func(c *Config) { c.BaseURL = "ftp://user:" + testPass + "@api.test" }, "base_url"},
		{"unparsable base_url with a password", func(c *Config) { c.BaseURL = "https://user:" + testPass + "@api.test/%zz" }, "base_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			_, err := newProvider(cfg, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), testPass) {
				t.Errorf("error leaks the password: %v", err)
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
