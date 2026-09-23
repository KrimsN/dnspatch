package selectel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	testAccount = "123456"
	testUser    = "user"
	testPass    = "s3cret"
	testProject = "default"
	testZoneID  = "zone-1"
)

// call is one DNS API request the fake received (the auth exchange is
// tracked separately).
type call struct {
	method string
	path   string
	query  url.Values
}

// fakeAPI is an in-memory stand-in for the identity service and the DNS API.
type fakeAPI struct {
	mu sync.Mutex

	rrsets map[string]*rrset // key: name+"/"+type
	nextID int

	calls     []call
	authCalls int
	token     string

	// failAuth makes the identity service refuse the next authentication.
	failAuth bool
	// failCalls makes the named method+path prefix answer with this status once.
	failCalls map[string]int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{rrsets: map[string]*rrset{}, failCalls: map[string]int{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/identity/v3/auth/tokens", api.handleAuth)
	mux.HandleFunc("/domains/v2/zones", api.handleZones)
	mux.HandleFunc("/domains/v2/zones/", api.handleZoneSub)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return api, srv
}

func (a *fakeAPI) handleAuth(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.authCalls++

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Auth struct {
			Identity struct {
				Password struct {
					User struct {
						Name     string                `json:"name"`
						Domain   struct{ Name string } `json:"domain"`
						Password string                `json:"password"`
					} `json:"user"`
				} `json:"password"`
			} `json:"identity"`
			Scope struct {
				Project struct {
					Name   string                `json:"name"`
					Domain struct{ Name string } `json:"domain"`
				} `json:"project"`
			} `json:"scope"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	user := body.Auth.Identity.Password.User
	valid := !a.failAuth &&
		user.Name == testUser && user.Password == testPass && user.Domain.Name == testAccount &&
		body.Auth.Scope.Project.Name == testProject && body.Auth.Scope.Project.Domain.Name == testAccount

	if !valid {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":{"message":"The request you have made requires authentication."}}`)
		return
	}

	a.token = fmt.Sprintf("token-%d", a.authCalls)
	w.Header().Set("X-Subject-Token", a.token)
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"token":{"expires_at":%q}}`, time.Now().Add(24*time.Hour).Format(time.RFC3339))
}

// authorized checks the caller's token and logs the call. It writes an error
// response and returns false when the request should not be served further.
func (a *fakeAPI) authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Auth-Token") != a.token || a.token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":{"message":"Bad or expired token."}}`)
		return false
	}

	if status, ok := a.failCalls[r.Method+" "+r.URL.Path]; ok {
		delete(a.failCalls, r.Method+" "+r.URL.Path)
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, `{"error":"forced failure"}`)
		return false
	}

	a.calls = append(a.calls, call{method: r.Method, path: r.URL.Path, query: r.URL.Query()})

	return true
}

func (a *fakeAPI) handleZones(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.authorized(w, r) {
		return
	}

	filter := r.URL.Query().Get("filter")
	result := []map[string]any{}
	if filter == "" || strings.Contains("example.com", filter) {
		result = append(result, map[string]any{"id": testZoneID, "name": "example.com"})
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"count": len(result), "result": result})
}

func (a *fakeAPI) handleZoneSub(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.authorized(w, r) {
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/domains/v2/zones/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] != testZoneID || parts[1] != "rrset" {
		http.NotFound(w, r)
		return
	}

	switch {
	case len(parts) == 2 && r.Method == http.MethodGet:
		a.listRRSets(w, r)
	case len(parts) == 2 && r.Method == http.MethodPost:
		a.createRRSet(w, r)
	case len(parts) == 3 && r.Method == http.MethodPatch:
		a.updateRRSet(w, r, parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (a *fakeAPI) listRRSets(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	recType := r.URL.Query().Get("rrset_types")

	result := []rrset{}
	if rr, ok := a.rrsets[name+"/"+recType]; ok {
		result = append(result, *rr)
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"count": len(result), "result": result})
}

func (a *fakeAPI) createRRSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		TTL     int      `json:"ttl"`
		Type    string   `json:"type"`
		Records []record `json:"records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	a.nextID++
	rr := rrset{ID: strconv.Itoa(a.nextID), Name: body.Name, TTL: body.TTL, Type: body.Type, Records: body.Records}
	a.rrsets[body.Name+"/"+body.Type] = &rr

	_ = json.NewEncoder(w).Encode(rr)
}

func (a *fakeAPI) updateRRSet(w http.ResponseWriter, r *http.Request, rrsetID string) {
	var body struct {
		TTL     int      `json:"ttl"`
		Records []record `json:"records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	for key, rr := range a.rrsets {
		if rr.ID == rrsetID {
			rr.TTL, rr.Records = body.TTL, body.Records
			a.rrsets[key] = rr
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	http.NotFound(w, r)
}

func (a *fakeAPI) methods() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	names := make([]string, len(a.calls))
	for i, c := range a.calls {
		path := strings.TrimPrefix(c.path, "/domains/v2")
		if q := c.query.Encode(); q != "" {
			path += "?" + q
		}
		names[i] = c.method + " " + path
	}

	return names
}

func (a *fakeAPI) putRRSet(name, recType string, ttl int, contents ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.nextID++
	records := make([]record, len(contents))
	for i, c := range contents {
		records[i] = record{Content: c}
	}
	a.rrsets[name+"/"+recType] = &rrset{ID: strconv.Itoa(a.nextID), Name: name, TTL: ttl, Type: recType, Records: records}
}

func testConfig(srv *httptest.Server) Config {
	return Config{
		AccountID: testAccount, Username: testUser, Password: testPass, ProjectName: testProject,
		Zone: "example.com", RRName: "home", TTL: 300,
		AuthURL: srv.URL + "/identity/v3", BaseURL: srv.URL + "/domains/v2",
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

func TestUpdateCreatesMissingRecord(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	rr := api.rrsets["home.example.com./A"]
	if rr == nil || len(rr.Records) != 1 || rr.Records[0].Content != "203.0.113.7" {
		t.Fatalf("rrset = %+v", rr)
	}
	if rr.TTL != 300 {
		t.Errorf("ttl = %d, want the configured 300", rr.TTL)
	}
}

func TestUpdateReplacesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRRSet("home.example.com.", "A", 120, "198.51.100.1")

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	rr := api.rrsets["home.example.com./A"]
	if len(rr.Records) != 1 || rr.Records[0].Content != "203.0.113.7" {
		t.Fatalf("rrset = %+v", rr)
	}
	if rr.TTL != 120 {
		t.Errorf("ttl = %d, want the existing 120 preserved", rr.TTL)
	}

	want := "GET /zones?filter=example.com GET /zones/zone-1/rrset?name=home.example.com.&rrset_types=A PATCH /zones/zone-1/rrset/1"
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdateSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRRSet("home.example.com.", "A", 60, "203.0.113.7")

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	want := "GET /zones?filter=example.com GET /zones/zone-1/rrset?name=home.example.com.&rrset_types=A"
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdateIPv6UsesAAAA(t *testing.T) {
	api, srv := newFakeAPI(t)

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
		t.Fatal(err)
	}

	rr := api.rrsets["home.example.com./AAAA"]
	if rr == nil || rr.Type != "AAAA" || rr.Records[0].Content != "2001:db8::7" {
		t.Fatalf("rrset = %+v", rr)
	}
}

func TestUpdateWritesBothFamiliesInOneCall(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4, V6: v6}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if rr := api.rrsets["home.example.com./A"]; rr == nil || rr.Records[0].Content != "203.0.113.7" {
		t.Errorf("A rrset = %+v", rr)
	}
	if rr := api.rrsets["home.example.com./AAAA"]; rr == nil || rr.Records[0].Content != "2001:db8::7" {
		t.Errorf("AAAA rrset = %+v", rr)
	}
}

func TestUpdateWithOneInvalidFamilyWritesOnlyTheOther(t *testing.T) {
	api, srv := newFakeAPI(t)

	addrs := plugin.Addresses{V4: v4}
	if err := mustProvider(t, testConfig(srv), srv).Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if api.rrsets["home.example.com./AAAA"] != nil {
		t.Error("AAAA rrset must not be created")
	}
	if rr := api.rrsets["home.example.com./A"]; rr == nil {
		t.Error("A rrset must be created")
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

	rr := api.rrsets["home.example.com./A"]
	if rr == nil || rr.TTL != 90 {
		t.Fatalf("ttl = %+v, want 90 from opts.TTL, not the configured 300", rr)
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
			api.putRRSet("home.example.com.", "AAAA", 60, content)

			if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v6); err != nil {
				t.Fatal(err)
			}
			want := "GET /zones?filter=example.com GET /zones/zone-1/rrset?name=home.example.com.&rrset_types=AAAA"
			if got := strings.Join(api.methods(), " "); got != want {
				t.Errorf("calls = %s, want %s", got, want)
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
	api.putRRSet("home.example.com.", "A", 60, "198.51.100.1", "198.51.100.2")

	err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
	if err == nil || !strings.Contains(err.Error(), "2 records") || !strings.Contains(err.Error(), "none is 203.0.113.7") {
		t.Fatalf("error = %v", err)
	}
	want := "GET /zones?filter=example.com GET /zones/zone-1/rrset?name=home.example.com.&rrset_types=A"
	if got := strings.Join(api.methods(), " "); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestUpdatePrunesStaleRecordsNextToCurrentOne(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.putRRSet("home.example.com.", "A", 60, "198.51.100.1", "203.0.113.7", "198.51.100.2")

	if err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4); err != nil {
		t.Fatal(err)
	}

	rr := api.rrsets["home.example.com./A"]
	if len(rr.Records) != 1 || rr.Records[0].Content != "203.0.113.7" {
		t.Fatalf("rrset = %+v, want only the wanted address left", rr)
	}
}

func TestUpdateCachesTheToken(t *testing.T) {
	api, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(srv), srv)

	for range 3 {
		if err := update(context.Background(), p, v4); err != nil {
			t.Fatal(err)
		}
	}

	if api.authCalls != 1 {
		t.Errorf("authCalls = %d, want exactly one authentication for three calls", api.authCalls)
	}
}

func TestUpdateReauthenticatesOn401(t *testing.T) {
	api, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(srv), srv)

	if err := update(context.Background(), p, v4); err != nil {
		t.Fatal(err)
	}
	if api.authCalls != 1 {
		t.Fatalf("authCalls = %d, want 1 after the first call", api.authCalls)
	}

	// The server-side token is invalidated behind the provider's back.
	api.mu.Lock()
	api.token = "revoked-" + api.token
	api.mu.Unlock()

	if err := update(context.Background(), p, v4); err != nil {
		t.Fatal(err)
	}
	if api.authCalls != 2 {
		t.Errorf("authCalls = %d, want a second authentication after the 401", api.authCalls)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Run("wrong password", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		cfg := testConfig(srv)
		cfg.Password = "wrong-password"

		err := update(context.Background(), mustProvider(t, cfg, srv), v4)
		if err == nil || !strings.Contains(err.Error(), "authentication failed") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "wrong-password") {
			t.Errorf("error leaks the password: %v", err)
		}
	})

	t.Run("http error status from the dns api", func(t *testing.T) {
		api, srv := newFakeAPI(t)
		api.failCalls["GET /domains/v2/zones"] = http.StatusBadGateway

		err := update(context.Background(), mustProvider(t, testConfig(srv), srv), v4)
		if err == nil || !strings.Contains(err.Error(), "502") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("zone not found", func(t *testing.T) {
		_, srv := newFakeAPI(t)
		cfg := testConfig(srv)
		cfg.Zone = "unknown.example"

		err := update(context.Background(), mustProvider(t, cfg, srv), v4)
		if err == nil || !strings.Contains(err.Error(), "not found") {
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

	cfg := Config{
		AccountID: testAccount, Username: testUser, Password: testPass, ProjectName: testProject,
		Zone: "example.com", RRName: "home", TTL: 60,
		AuthURL: srv.URL, BaseURL: srv.URL,
	}

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
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body != nil {
			leaked.Store(true)
		}
		http.Error(w, "should not be reached", http.StatusTeapot)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.RedirectHandler(target.URL+"/identity/v3/auth/tokens", http.StatusTemporaryRedirect))
	defer origin.Close()

	cfg := testConfig(origin)
	cfg.AuthURL = origin.URL

	err := update(context.Background(), mustProvider(t, cfg, origin), v4)
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("error = %v, want the redirect reported as an unexpected status", err)
	}
	if leaked.Load() {
		t.Error("the password was sent to the redirect target")
	}
}

func TestNewProviderValidation(t *testing.T) {
	valid := Config{
		AccountID: testAccount, Username: testUser, Password: testPass, ProjectName: testProject,
		Zone: "example.com", RRName: "home", TTL: 300,
		AuthURL: "https://identity.test", BaseURL: "https://api.test",
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
		{"ttl too low", func(c *Config) { c.TTL = 1 }, "ttl"},
		{"ttl too high", func(c *Config) { c.TTL = 1_000_000 }, "ttl"},
		{"bad auth_url", func(c *Config) { c.AuthURL = "identity.test" }, "auth_url"},
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
		"account_id": testAccount, "username": testUser, "password": testPass, "project_name": testProject,
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
