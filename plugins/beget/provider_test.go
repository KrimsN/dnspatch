package beget

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	testLogin  = "user"
	testPass   = "s3cret"
	testDomain = "home.example.com"
)

// fakeAPI is an in-memory stand-in for the Beget API. It stores one record
// set (every type at once) per fqdn, the way dns/changeRecords replaces it.
type fakeAPI struct {
	mu sync.Mutex

	records map[string]map[string]json.RawMessage

	calls        []string // method, in call order
	getDataCalls int

	// failNextChange makes the next dns/changeRecords answer with this
	// API-level error code instead of succeeding.
	failNextChange string
	// failStatus makes the next call answer with this HTTP status.
	failStatus int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{records: map[string]map[string]json.RawMessage{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/dns/getData", api.handleGetData)
	mux.HandleFunc("/api/dns/changeRecords", api.handleChangeRecords)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return api, srv
}

func (a *fakeAPI) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("login") != testLogin || r.URL.Query().Get("passwd") != testPass {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"error","error_code":"AUTH_ERROR","error_text":"invalid login or password"}`)
		return false
	}

	return true
}

func (a *fakeAPI) handleGetData(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.checkAuth(w, r) {
		return
	}

	a.calls = append(a.calls, "GET dns/getData")
	a.getDataCalls++

	if a.failStatus != 0 {
		status := a.failStatus
		a.failStatus = 0
		w.WriteHeader(status)
		return
	}

	var input struct {
		Fqdn string `json:"fqdn"`
	}
	_ = json.Unmarshal([]byte(r.URL.Query().Get("input_data")), &input)

	records := a.records[input.Fqdn]
	if records == nil {
		records = map[string]json.RawMessage{}
	}

	body, _ := json.Marshal(map[string]any{
		"status": "success",
		"answer": map[string]any{
			"status": "success",
			"result": map[string]any{"fqdn": input.Fqdn, "records": records},
		},
	})
	_, _ = w.Write(body)
}

func (a *fakeAPI) handleChangeRecords(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.checkAuth(w, r) {
		return
	}

	a.calls = append(a.calls, "GET dns/changeRecords")

	if a.failNextChange != "" {
		code := a.failNextChange
		a.failNextChange = ""
		body, _ := json.Marshal(map[string]any{
			"status": "success",
			"answer": map[string]any{"status": "error", "error_code": code, "error_text": "forced failure"},
		})
		_, _ = w.Write(body)
		return
	}

	var input struct {
		Fqdn    string                     `json:"fqdn"`
		Records map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal([]byte(r.URL.Query().Get("input_data")), &input); err != nil {
		http.Error(w, "bad input_data", http.StatusBadRequest)
		return
	}

	a.records[input.Fqdn] = input.Records

	body, _ := json.Marshal(map[string]any{
		"status": "success",
		"answer": map[string]any{"status": "success", "result": true},
	})
	_, _ = w.Write(body)
}

// putRecords seeds the full record set of an fqdn the way changeRecords
// would have left it.
func (a *fakeAPI) putRecords(fqdn string, recs map[string][]record) {
	a.mu.Lock()
	defer a.mu.Unlock()

	set := map[string]json.RawMessage{}
	for k, v := range recs {
		encoded, _ := json.Marshal(v)
		set[k] = encoded
	}
	a.records[fqdn] = set
}

func (a *fakeAPI) recordsOf(fqdn, recType string) []record {
	a.mu.Lock()
	defer a.mu.Unlock()

	var out []record
	if raw, ok := a.records[fqdn][recType]; ok {
		_ = json.Unmarshal(raw, &out)
	}

	return out
}

func testConfig(srv *httptest.Server) Config {
	return Config{
		Login: testLogin, Password: testPass,
		Zone: "example.com", RRName: "home",
		BaseURL: srv.URL + "/api",
	}
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

func update(ctx context.Context, p *provider, addr netip.Addr) error {
	addrs := plugin.Addresses{}
	if addr.Is4() {
		addrs.V4 = addr
	} else {
		addrs.V6 = addr
	}
	return p.Update(ctx, addrs, plugin.RecordOptions{})
}

func TestUpdateCreatesMissingRecord(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	recs := api.recordsOf(testDomain, "A")
	if len(recs) != 1 || recs[0].Value != "203.0.113.7" {
		t.Fatalf("A records = %+v", recs)
	}
}

func TestUpdateReplacesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRecords(testDomain, map[string][]record{"A": {{Value: "198.51.100.1", Priority: 20}}})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	recs := api.recordsOf(testDomain, "A")
	if len(recs) != 1 || recs[0].Value != "203.0.113.7" {
		t.Fatalf("A records = %+v", recs)
	}
}

func TestUpdateSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRecords(testDomain, map[string][]record{"A": {{Value: "203.0.113.7", Priority: 10}}})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	if got := len(api.calls); got != 1 {
		t.Errorf("calls = %v, want only the getData lookup, no write", api.calls)
	}
}

func TestUpdateIPv6UsesAAAA(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
		t.Fatal(err)
	}

	recs := api.recordsOf(testDomain, "AAAA")
	if len(recs) != 1 || recs[0].Value != "2001:db8::7" {
		t.Fatalf("AAAA records = %+v", recs)
	}
}

func TestUpdatePreservesOtherRecordTypes(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRecords(testDomain, map[string][]record{
		"MX":  {{Value: "mx1.example.com", Priority: 10}},
		"TXT": {{Value: "v=spf1 -all"}},
	})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	if mx := api.recordsOf(testDomain, "MX"); len(mx) != 1 || mx[0].Value != "mx1.example.com" {
		t.Errorf("MX records = %+v, want the existing MX preserved", mx)
	}
	if txt := api.recordsOf(testDomain, "TXT"); len(txt) != 1 || txt[0].Value != "v=spf1 -all" {
		t.Errorf("TXT records = %+v, want the existing TXT preserved", txt)
	}
}

func TestUpdateWritesBothFamiliesInOneCall(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4, V6: v6}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if recs := api.recordsOf(testDomain, "A"); len(recs) != 1 || recs[0].Value != "203.0.113.7" {
		t.Errorf("A records = %+v", recs)
	}
	if recs := api.recordsOf(testDomain, "AAAA"); len(recs) != 1 || recs[0].Value != "2001:db8::7" {
		t.Errorf("AAAA records = %+v", recs)
	}
}

func TestUpdateWithOneInvalidFamilyWritesOnlyTheOther(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if recs := api.recordsOf(testDomain, "AAAA"); recs != nil {
		t.Error("AAAA record must not be created")
	}
	if recs := api.recordsOf(testDomain, "A"); len(recs) != 1 {
		t.Error("A record must be created")
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
			api, srv := newFakeAPI(t)
			api.putRecords(testDomain, map[string][]record{"AAAA": {{Value: content, Priority: 10}}})

			if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
				t.Fatal(err)
			}
			if got := len(api.calls); got != 1 {
				t.Errorf("calls = %v, want no write for an address that is already current", api.calls)
			}
		})
	}
}

func TestUpdateApexAndWildcard(t *testing.T) {
	for _, label := range []string{"@", "*"} {
		t.Run(label, func(t *testing.T) {
			_, srv := newFakeAPI(t)

			cfg := testConfig(srv)
			cfg.RRName = label
			if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpdateRefusesAmbiguousRecords(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRecords(testDomain, map[string][]record{"A": {{Value: "198.51.100.1"}, {Value: "198.51.100.2"}}})

	err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
	if err == nil || !strings.Contains(err.Error(), "2 A records") || !strings.Contains(err.Error(), "none is 203.0.113.7") {
		t.Fatalf("error = %v", err)
	}
	if got := len(api.calls); got != 1 {
		t.Errorf("calls = %v, want no write when the record is ambiguous", api.calls)
	}
}

func TestUpdatePrunesStaleRecordsNextToCurrentOne(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRecords(testDomain, map[string][]record{"A": {{Value: "198.51.100.1"}, {Value: "203.0.113.7"}, {Value: "198.51.100.2"}}})

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	recs := api.recordsOf(testDomain, "A")
	if len(recs) != 1 || recs[0].Value != "203.0.113.7" {
		t.Fatalf("A records = %+v, want only the wanted address left", recs)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Run("wrong password", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		cfg := testConfig(srv)
		cfg.Password = "wrong-password"

		err := update(context.Background(), mustProvider(t, cfg, srv), v4)
		if err == nil || !strings.Contains(err.Error(), "AUTH_ERROR") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "wrong-password") {
			t.Errorf("error leaks the password: %v", err)
		}
	})

	t.Run("http error status from the api", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failStatus = http.StatusBadGateway

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "502") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("changeRecords rejected by the api", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failNextChange = "RATE_LIMIT"

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "RATE_LIMIT") {
			t.Fatalf("error = %v", err)
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

	cfg := Config{Login: testLogin, Password: testPass, Zone: "example.com", RRName: "home", BaseURL: srv.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := update(ctx, mustProvider(t, cfg, srv), v4)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testPass) {
		t.Errorf("error leaks the password: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want prompt return on cancelled context", elapsed)
	}
}

func TestUpdateUnreachableDoesNotLeakPassword(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	cfg := Config{Login: testLogin, Password: testPass, Zone: "example.com", RRName: "home", BaseURL: "http://" + addr}

	p, err := newProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = update(context.Background(), p, v4)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), testPass) || strings.Contains(err.Error(), url.QueryEscape(testPass)) {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestUpdateDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be reached", http.StatusTeapot)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path+"?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	cfg := testConfig(origin)
	cfg.BaseURL = origin.URL

	err := update(context.Background(), mustProvider(t, cfg, origin), v4)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("error = %v, want the redirect reported as an unexpected status", err)
	}
	if strings.Contains(err.Error(), testPass) {
		t.Error("the password was leaked through the redirect error")
	}
}

func TestNewProviderValidation(t *testing.T) {
	valid := Config{
		Login: testLogin, Password: testPass,
		Zone: "example.com", RRName: "home",
		BaseURL: "https://api.test",
	}

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

func TestRegisteredInDefault(t *testing.T) {
	params := map[string]any{
		"login": testLogin, "password": testPass,
		"zone": "example.com", "rr_name": "@",
	}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatal(err)
	}

	delete(params, "password")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("error = %v, want a missing password complaint", err)
	}
}
