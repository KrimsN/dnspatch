package timeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

const testToken = "tw-test-token"

// fakeAPI is an in-memory stand-in for the Timeweb Cloud DNS API.
type fakeAPI struct {
	mu sync.Mutex

	records map[int]record
	nextID  int

	calls []string
	// failCalls makes the named method+path answer with this status once.
	failCalls map[string]int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{records: map[int]record{}, failCalls: map[string]int{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/domains/example.com/dns-records", api.handleCollection)
	mux.HandleFunc("/domains/example.com/dns-records/", api.handleItem)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return api, srv
}

// authorized checks the caller's token and logs the call. It writes an error
// response and returns false when the request should not be served further.
func (a *fakeAPI) authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"message":"Unauthorized","error_code":"unauthorized"}`)
		return false
	}

	key := r.Method + " " + r.URL.Path
	if status, ok := a.failCalls[key]; ok {
		delete(a.failCalls, key)
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, `{"message":"forced failure","error_code":"forced"}`)
		return false
	}

	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	a.calls = append(a.calls, key)

	return true
}

func (a *fakeAPI) handleCollection(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.authorized(w, r) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.list(w)
	case http.MethodPost:
		a.create(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *fakeAPI) handleItem(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.authorized(w, r) {
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/domains/example.com/dns-records/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rec, ok := a.records[id]
	if !ok {
		http.NotFound(w, r)
		return
	}

	var body writeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	rec.Type = body.Type
	rec.Data.Value = body.Value
	rec.Data.SubDomain = body.SubDomain
	if body.TTL != 0 {
		rec.TTL = body.TTL
	}
	a.records[id] = rec

	_ = json.NewEncoder(w).Encode(writeResponse{DNSRecord: rec})
}

func (a *fakeAPI) list(w http.ResponseWriter) {
	records := make([]record, 0, len(a.records))
	for _, r := range a.records {
		records = append(records, r)
	}

	_ = json.NewEncoder(w).Encode(recordList{
		DNSRecords: records,
		Meta: struct {
			Total int `json:"total"`
		}{Total: len(records)},
	})
}

func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request) {
	var body writeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	a.nextID++
	rec := record{
		ID:   a.nextID,
		Type: body.Type,
		TTL:  body.TTL,
		Data: recordData{SubDomain: body.SubDomain, Value: body.Value},
	}
	a.records[rec.ID] = rec

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(writeResponse{DNSRecord: rec})
}

// put seeds an existing record directly, bypassing the HTTP API.
func (a *fakeAPI) put(recType, sub, value string, ttl int) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.nextID++
	a.records[a.nextID] = record{
		ID:   a.nextID,
		Type: recType,
		TTL:  ttl,
		Data: recordData{SubDomain: sub, Value: value},
	}
	return a.nextID
}

func (a *fakeAPI) methods() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]string, len(a.calls))
	copy(out, a.calls)
	return out
}

func testConfig(srv *httptest.Server) Config {
	return Config{
		Token: testToken, Zone: "example.com", RRName: "home", TTL: 300,
		BaseURL: srv.URL,
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

	if len(api.records) != 1 {
		t.Fatalf("records = %+v, want exactly one", api.records)
	}
	for _, r := range api.records {
		if r.Type != "A" || r.Data.Value != "203.0.113.7" || r.Data.SubDomain != "home" || r.TTL != 300 {
			t.Errorf("record = %+v", r)
		}
	}
}

func TestUpdateReplacesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	id := api.put("A", "home", "198.51.100.1", 120)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	rec := api.records[id]
	if rec.Data.Value != "203.0.113.7" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.TTL != 120 {
		t.Errorf("ttl = %d, want the existing 120 preserved", rec.TTL)
	}

	want := "GET /domains/example.com/dns-records?limit=500 PATCH /domains/example.com/dns-records/" + strconv.Itoa(id)
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdateSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.put("A", "home", "203.0.113.7", 60)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	want := "GET /domains/example.com/dns-records?limit=500"
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdateIPv6UsesAAAA(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, r := range api.records {
		if r.Type == "AAAA" && r.Data.Value == "2001:db8::7" {
			found = true
		}
	}
	if !found {
		t.Errorf("records = %+v, want an AAAA record", api.records)
	}
}

func TestUpdateWritesBothFamiliesInOneCall(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4, V6: v6}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	var gotA, gotAAAA bool
	for _, r := range api.records {
		if r.Type == "A" && r.Data.Value == "203.0.113.7" {
			gotA = true
		}
		if r.Type == "AAAA" && r.Data.Value == "2001:db8::7" {
			gotAAAA = true
		}
	}
	if !gotA || !gotAAAA {
		t.Errorf("records = %+v, want both an A and an AAAA record", api.records)
	}
}

func TestUpdateWithOneInvalidFamilyWritesOnlyTheOther(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, r := range api.records {
		if r.Type == "AAAA" {
			t.Error("AAAA record must not be created")
		}
	}
	if len(api.records) != 1 {
		t.Errorf("records = %+v, want exactly one A record", api.records)
	}
}

func TestUpdateWithNoValidAddressFails(t *testing.T) {
	_, srv := newFakeAPI(t)

	err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), plugin.Addresses{}, plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "no address to write") {
		t.Fatalf("error = %v", err)
	}
}

func TestUpdateOptsTTLOverridesConfiguredTTL(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4}
	opts := plugin.RecordOptions{TTL: 90 * time.Second}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, opts); err != nil {
		t.Fatal(err)
	}

	for _, r := range api.records {
		if r.TTL != 90 {
			t.Errorf("ttl = %+v, want 90 from opts.TTL, not the configured 300", r)
		}
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
			api.put("AAAA", "home", content, 60)

			if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
				t.Fatal(err)
			}
			want := "GET /domains/example.com/dns-records?limit=500"
			if got := strings.Join(api.methods(), " "); got != want {
				t.Errorf("calls = %s, want %s", got, want)
			}
		})
	}
}

func TestUpdateApexUsesEmptySubdomain(t *testing.T) {
	api, srv := newFakeAPI(t)

	cfg := testConfig(srv)
	cfg.RRName = "@"
	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}

	for _, r := range api.records {
		if r.Data.SubDomain != "" {
			t.Errorf("subdomain = %q, want empty for the apex", r.Data.SubDomain)
		}
	}
}

func TestUpdateWildcard(t *testing.T) {
	_, srv := newFakeAPI(t)

	cfg := testConfig(srv)
	cfg.RRName = "*"
	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDoesNotConfuseDifferentSubdomains(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.put("A", "office", "198.51.100.1", 60)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	if len(api.records) != 2 {
		t.Fatalf("records = %+v, want the office record left alone and a new home record created", api.records)
	}
}

func TestUpdateRefusesAmbiguousRecords(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.put("A", "home", "198.51.100.1", 60)
	api.put("A", "home", "198.51.100.2", 60)

	err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
	if err == nil || !strings.Contains(err.Error(), "2 A records") || !strings.Contains(err.Error(), "none is 203.0.113.7") {
		t.Fatalf("error = %v", err)
	}
	want := "GET /domains/example.com/dns-records?limit=500"
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Run("wrong token", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		cfg := testConfig(srv)
		cfg.Token = "wrong-token"

		err := update(context.Background(), mustProvider(t, cfg, srv), v4)
		if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("http error status from the api", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failCalls["GET /domains/example.com/dns-records"] = http.StatusBadGateway

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "502") {
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

	cfg := testConfig(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := update(ctx, mustProvider(t, cfg, srv), v4); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("returned after %s, want prompt return on cancelled context", elapsed)
	}
}

func TestUpdateDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		http.Error(w, "should not be reached", http.StatusTeapot)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusTemporaryRedirect))
	defer origin.Close()

	cfg := testConfig(origin)

	err := update(context.Background(), mustProvider(t, cfg, origin), v4)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("error = %v, want the redirect reported as an unexpected status", err)
	}
	if leaked.Load() {
		t.Error("the token was sent to the redirect target")
	}
}

func TestNewProviderValidation(t *testing.T) {
	valid := Config{
		Token: testToken, Zone: "example.com", RRName: "home", TTL: 300,
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
		{"negative ttl", func(c *Config) { c.TTL = -1 }, "ttl"},
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
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("error leaks the token: %v", err)
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
		"token": testToken, "zone": "example.com", "rr_name": "@",
	}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatal(err)
	}

	delete(params, "token")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("error = %v, want a missing token complaint", err)
	}
}
