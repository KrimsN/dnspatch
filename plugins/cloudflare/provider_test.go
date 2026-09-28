package cloudflare

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

const (
	testToken  = "cf-test-token"
	testZoneID = "023e105f4ecef8ad9ca31a8372d0c353"
)

// fakeAPI is an in-memory stand-in for the Cloudflare API.
type fakeAPI struct {
	mu sync.Mutex

	records map[string]record
	nextID  int

	calls []string
	// failCalls makes the named method+path answer with this status once.
	failCalls map[string]int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{records: map[string]record{}, failCalls: map[string]int{}}

	mux := http.NewServeMux()
	prefix := "/zones/" + testZoneID + "/dns_records"
	mux.HandleFunc(prefix, api.handleCollection)
	mux.HandleFunc(prefix+"/", api.handleItem)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return api, srv
}

// authorized checks the caller's token and logs the call. It writes an error
// response and returns false when the request should not be served further.
func (a *fakeAPI) authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}],"result":null}`)
		return false
	}

	key := r.Method + " " + r.URL.Path
	if status, ok := a.failCalls[key]; ok {
		delete(a.failCalls, key)
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, `{"success":false,"errors":[{"code":1000,"message":"forced failure"}],"result":null}`)
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
		a.list(w, r)
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

	prefix := "/zones/" + testZoneID + "/dns_records/"
	id := strings.TrimPrefix(r.URL.Path, prefix)

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
	rec.Name = body.Name
	rec.Content = body.Content
	rec.Proxied = body.Proxied
	if body.TTL != 0 {
		rec.TTL = body.TTL
	}
	a.records[id] = rec

	writeEnvelope(w, rec)
}

func (a *fakeAPI) list(w http.ResponseWriter, r *http.Request) {
	wantType := r.URL.Query().Get("type")
	wantName := r.URL.Query().Get("name")

	var matched []record
	for _, r := range a.records {
		if (wantType == "" || r.Type == wantType) && (wantName == "" || strings.EqualFold(r.Name, wantName)) {
			matched = append(matched, r)
		}
	}
	if matched == nil {
		matched = []record{}
	}

	env := struct {
		Success    bool     `json:"success"`
		Errors     []any    `json:"errors"`
		Result     []record `json:"result"`
		ResultInfo struct {
			Count      int `json:"count"`
			TotalCount int `json:"total_count"`
		} `json:"result_info"`
	}{Success: true, Errors: []any{}, Result: matched}
	env.ResultInfo.Count = len(matched)
	env.ResultInfo.TotalCount = len(matched)

	_ = json.NewEncoder(w).Encode(env)
}

func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request) {
	var body writeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	a.nextID++
	rec := record{
		ID:      strconv.Itoa(a.nextID),
		Type:    body.Type,
		Name:    body.Name,
		Content: body.Content,
		TTL:     body.TTL,
		Proxied: body.Proxied,
	}
	a.records[rec.ID] = rec

	w.WriteHeader(http.StatusOK)
	writeEnvelope(w, rec)
}

func writeEnvelope(w http.ResponseWriter, rec record) {
	env := struct {
		Success bool   `json:"success"`
		Errors  []any  `json:"errors"`
		Result  record `json:"result"`
	}{Success: true, Errors: []any{}, Result: rec}
	_ = json.NewEncoder(w).Encode(env)
}

// put seeds an existing record directly, bypassing the HTTP API.
func (a *fakeAPI) put(recType, name, content string, ttl int) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.nextID++
	id := strconv.Itoa(a.nextID)
	a.records[id] = record{ID: id, Type: recType, Name: name, Content: content, TTL: ttl}
	return id
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
		Token: testToken, ZoneID: testZoneID, Zone: "example.com", RRName: "home", TTL: 300,
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
		if r.Type != "A" || r.Content != "203.0.113.7" || r.Name != "home.example.com" || r.TTL != 300 {
			t.Errorf("record = %+v", r)
		}
	}
}

func TestUpdateReplacesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	id := api.put("A", "home.example.com", "198.51.100.1", 120)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	rec := api.records[id]
	if rec.Content != "203.0.113.7" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.TTL != 120 {
		t.Errorf("ttl = %d, want the existing 120 preserved", rec.TTL)
	}

	want := "GET /zones/" + testZoneID + "/dns_records?type=A&name=home.example.com&per_page=100 PATCH /zones/" + testZoneID + "/dns_records/" + id
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdateSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.put("A", "home.example.com", "203.0.113.7", 60)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	want := "GET /zones/" + testZoneID + "/dns_records?type=A&name=home.example.com&per_page=100"
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
		if r.Type == "AAAA" && r.Content == "2001:db8::7" {
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
		if r.Type == "A" && r.Content == "203.0.113.7" {
			gotA = true
		}
		if r.Type == "AAAA" && r.Content == "2001:db8::7" {
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

func TestUpdateCreatesWithConfiguredProxied(t *testing.T) {
	api, srv := newFakeAPI(t)
	cfg := testConfig(srv)
	cfg.Proxied = true

	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}

	for _, r := range api.records {
		if !r.Proxied {
			t.Errorf("record = %+v, want proxied true", r)
		}
	}
}

func TestUpdateKeepsExistingProxiedSetting(t *testing.T) {
	api, srv := newFakeAPI(t)
	id := api.put("A", "home.example.com", "198.51.100.1", 60)
	rec := api.records[id]
	rec.Proxied = true
	api.records[id] = rec

	cfg := testConfig(srv)
	cfg.Proxied = false
	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}

	if !api.records[id].Proxied {
		t.Errorf("record = %+v, want the existing proxied=true preserved", api.records[id])
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
			api.put("AAAA", "home.example.com", content, 60)

			if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
				t.Fatal(err)
			}
			want := "GET /zones/" + testZoneID + "/dns_records?type=AAAA&name=home.example.com&per_page=100"
			if got := strings.Join(api.methods(), " "); got != want {
				t.Errorf("calls = %s, want %s", got, want)
			}
		})
	}
}

func TestUpdateApexUsesZoneName(t *testing.T) {
	api, srv := newFakeAPI(t)

	cfg := testConfig(srv)
	cfg.RRName = "@"
	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}

	for _, r := range api.records {
		if r.Name != "example.com" {
			t.Errorf("name = %q, want the bare zone for the apex", r.Name)
		}
	}
}

func TestUpdateWildcard(t *testing.T) {
	api, srv := newFakeAPI(t)

	cfg := testConfig(srv)
	cfg.RRName = "*"
	if err := update(context.Background(), mustProvider(t, cfg, srv), v4); err != nil {
		t.Fatal(err)
	}

	for _, r := range api.records {
		if r.Name != "*.example.com" {
			t.Errorf("name = %q, want *.example.com", r.Name)
		}
	}
}

func TestUpdateDoesNotConfuseDifferentNames(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.put("A", "office.example.com", "198.51.100.1", 60)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	if len(api.records) != 2 {
		t.Fatalf("records = %+v, want the office record left alone and a new home record created", api.records)
	}
}

func TestUpdateRefusesAmbiguousRecords(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.put("A", "home.example.com", "198.51.100.1", 60)
	api.put("A", "home.example.com", "198.51.100.2", 60)

	err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
	if err == nil || !strings.Contains(err.Error(), "2 A records") || !strings.Contains(err.Error(), "none is 203.0.113.7") {
		t.Fatalf("error = %v", err)
	}
	want := "GET /zones/" + testZoneID + "/dns_records?type=A&name=home.example.com&per_page=100"
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
		if err == nil || !strings.Contains(err.Error(), "Invalid access token") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("http error status from the api", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failCalls["GET /zones/"+testZoneID+"/dns_records"] = http.StatusBadGateway

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "forced failure") {
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
		Token: testToken, ZoneID: testZoneID, Zone: "example.com", RRName: "home", TTL: 300,
		BaseURL: "https://api.test",
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"empty zone_id", func(c *Config) { c.ZoneID = " " }, "zone_id"},
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
		"token": testToken, "zone_id": testZoneID, "zone": "example.com", "rr_name": "@",
	}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatal(err)
	}

	delete(params, "token")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("error = %v, want a missing token complaint", err)
	}
}
