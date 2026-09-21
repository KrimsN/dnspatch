package selectelv1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

const testToken = "secret-token"

// fakeAPI is an in-memory stand-in for the records endpoints of the API.
type fakeAPI struct {
	mu      sync.Mutex
	records []record
	nextID  int64
	// writes lists the "METHOD path" of every write request received.
	writes []string
	// bodies holds the decoded payload of every write request.
	bodies []recordBody
}

func newFakeAPI(t *testing.T, zone string, records ...record) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{records: records, nextID: 1000}
	prefix := "/" + zone + "/records"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()

		if r.Header.Get("X-Token") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid token"}`))
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == prefix:
			api.list(w, r)
		case r.Method == http.MethodPost && r.URL.Path == prefix:
			api.create(w, r)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, prefix+"/"):
			api.update(w, r, strings.TrimPrefix(r.URL.Path, prefix+"/"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no such route"}`))
		}
	}))
	t.Cleanup(srv.Close)

	return api, srv
}

func (a *fakeAPI) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	var matched []record
	for _, rec := range a.records {
		if types := q["record_types"]; len(types) > 0 && types[0] != rec.Type {
			continue
		}
		matched = append(matched, rec)
	}

	if offset > len(matched) {
		offset = len(matched)
	}
	end := min(offset+limit, len(matched))

	page := matched[offset:end]
	if page == nil {
		page = []record{}
	}
	_ = json.NewEncoder(w).Encode(page)
}

func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request) {
	var body recordBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	a.nextID++
	a.writes = append(a.writes, "POST "+r.URL.Path)
	a.bodies = append(a.bodies, body)
	a.records = append(a.records, record{ID: a.nextID, Type: body.Type, Name: body.Name, Content: body.Content, TTL: body.TTL})

	_ = json.NewEncoder(w).Encode(a.records[len(a.records)-1])
}

func (a *fakeAPI) update(w http.ResponseWriter, r *http.Request, idText string) {
	id, _ := strconv.ParseInt(idText, 10, 64)

	var body recordBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	a.writes = append(a.writes, "PUT "+r.URL.Path)
	a.bodies = append(a.bodies, body)

	for i, rec := range a.records {
		if rec.ID == id {
			a.records[i] = record{ID: id, Type: body.Type, Name: body.Name, Content: body.Content, TTL: body.TTL}
			_ = json.NewEncoder(w).Encode(a.records[i])
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"record not found"}`))
}

func testConfig(srv *httptest.Server) Config {
	return Config{
		APIToken: testToken,
		Zone:     "example.com",
		RRName:   "home",
		TTL:      300,
		BaseURL:  srv.URL,
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

func TestSetIPAddressCreatesMissingRecord(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com")
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}

	want := recordBody{Type: "A", Name: "home.example.com", Content: "203.0.113.7", TTL: 300}
	if len(api.bodies) != 1 || api.bodies[0] != want || api.writes[0] != "POST /example.com/records" {
		t.Errorf("writes = %v, bodies = %+v, want one POST of %+v", api.writes, api.bodies, want)
	}
}

func TestSetIPAddressUpdatesChangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com",
		record{ID: 7, Type: "A", Name: "home.example.com", Content: "198.51.100.1", TTL: 300},
		record{ID: 8, Type: "A", Name: "other.example.com", Content: "198.51.100.2", TTL: 300},
	)
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}

	if len(api.writes) != 1 || api.writes[0] != "PUT /example.com/records/7" {
		t.Fatalf("writes = %v, want one PUT of record 7", api.writes)
	}
	if got := api.records[0].Content; got != "203.0.113.7" {
		t.Errorf("record content = %s, want the new address", got)
	}
	if got := api.records[1].Content; got != "198.51.100.2" {
		t.Errorf("neighbour record was touched: %s", got)
	}
}

func TestSetIPAddressSkipsUnchangedRecord(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com",
		record{ID: 7, Type: "A", Name: "home.example.com", Content: "203.0.113.7", TTL: 300},
	)
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 0 {
		t.Errorf("writes = %v, want none", api.writes)
	}
}

func TestSetIPAddressUpdatesChangedTTL(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com",
		record{ID: 7, Type: "A", Name: "home.example.com", Content: "203.0.113.7", TTL: 60},
	)
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 1 || api.records[0].TTL != 300 {
		t.Errorf("writes = %v, ttl = %d, want one PUT setting ttl 300", api.writes, api.records[0].TTL)
	}
}

func TestSetIPAddressIPv6UsesAAAA(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com",
		record{ID: 7, Type: "A", Name: "home.example.com", Content: "203.0.113.7", TTL: 300},
	)
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v6); err != nil {
		t.Fatal(err)
	}

	if len(api.bodies) != 1 || api.bodies[0].Type != "AAAA" || api.bodies[0].Content != "2001:db8::7" {
		t.Errorf("bodies = %+v, want one AAAA write", api.bodies)
	}
	if api.records[0].Content != "203.0.113.7" {
		t.Error("the A record must be left alone when writing an IPv6 address")
	}
}

func TestSetIPAddressMatchesNamesLeniently(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com",
		record{ID: 7, Type: "A", Name: "HOME.Example.com.", Content: "198.51.100.1", TTL: 300},
	)
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 1 || !strings.HasPrefix(api.writes[0], "PUT") {
		t.Errorf("writes = %v, want the existing record updated, not a new one created", api.writes)
	}
}

func TestSetIPAddressRefusesAmbiguousRecords(t *testing.T) {
	api, srv := newFakeAPI(t, "example.com",
		record{ID: 7, Type: "A", Name: "home.example.com", Content: "198.51.100.1", TTL: 300},
		record{ID: 8, Type: "A", Name: "home.example.com", Content: "198.51.100.2", TTL: 300},
	)
	p := mustProvider(t, testConfig(srv), srv)

	err := p.SetIPAddress(context.Background(), v4)
	if err == nil || !strings.Contains(err.Error(), "2 A records") {
		t.Fatalf("error = %v, want a complaint about two records", err)
	}
	if len(api.writes) != 0 {
		t.Errorf("writes = %v, want none", api.writes)
	}
}

func TestSetIPAddressReadsAllPages(t *testing.T) {
	records := make([]record, 0, pageSize+1)
	for i := range pageSize {
		records = append(records, record{ID: int64(i + 1), Type: "A", Name: "host" + strconv.Itoa(i) + ".example.com", Content: "198.51.100.1", TTL: 300})
	}
	// The record we care about sits on the second page.
	records = append(records, record{ID: 5000, Type: "A", Name: "home.example.com", Content: "198.51.100.9", TTL: 300})

	api, srv := newFakeAPI(t, "example.com", records...)
	p := mustProvider(t, testConfig(srv), srv)

	if err := p.SetIPAddress(context.Background(), v4); err != nil {
		t.Fatal(err)
	}
	if len(api.writes) != 1 || api.writes[0] != "PUT /example.com/records/5000" {
		t.Errorf("writes = %v, want a PUT of record 5000", api.writes)
	}
}

func TestSetIPAddressErrors(t *testing.T) {
	t.Run("wrong token includes the response body", func(t *testing.T) {
		_, srv := newFakeAPI(t, "example.com")
		cfg := testConfig(srv)
		cfg.APIToken = "wrong"

		err := mustProvider(t, cfg, srv).SetIPAddress(context.Background(), v4)
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, want := range []string{"401", "invalid token", "api_token"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("failed write includes the response body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":"bad ttl","field":"ttl"}`))
		}))
		defer srv.Close()

		err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
		if err == nil || !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "bad ttl") {
			t.Errorf("error = %v, want status and body", err)
		}
	})

	t.Run("garbage list response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html>maintenance</html>`))
		}))
		defer srv.Close()

		err := mustProvider(t, testConfig(srv), srv).SetIPAddress(context.Background(), v4)
		if err == nil || !strings.Contains(err.Error(), "decoding response") {
			t.Errorf("error = %v, want a decoding error", err)
		}
	})

	t.Run("invalid address", func(t *testing.T) {
		_, srv := newFakeAPI(t, "example.com")
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

func TestRecordName(t *testing.T) {
	tests := []struct {
		zone, label, want string
	}{
		{"example.com", "@", "example.com"},
		{"Example.COM.", "@", "example.com"},
		{"example.com", "*", "*.example.com"},
		{"example.com", "Home", "home.example.com"},
		{"example.com", "a.b", "a.b.example.com"},
	}

	for _, tt := range tests {
		p, err := newProvider(Config{APIToken: "t", Zone: tt.zone, RRName: tt.label, TTL: 60, BaseURL: "https://api.test"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.name != tt.want {
			t.Errorf("zone %q label %q: name = %q, want %q", tt.zone, tt.label, p.name, tt.want)
		}
	}
}

func TestNewProviderValidation(t *testing.T) {
	valid := Config{APIToken: "t", Zone: "example.com", RRName: "home", TTL: 60, BaseURL: "https://api.test"}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"empty zone", func(c *Config) { c.Zone = "" }, "zone"},
		{"zone with a slash", func(c *Config) { c.Zone = "example.com/records" }, "zone"},
		{"empty rr_name", func(c *Config) { c.RRName = " " }, "rr_name"},
		{"rr_name with a trailing dot", func(c *Config) { c.RRName = "home." }, "rr_name"},
		{"zero ttl", func(c *Config) { c.TTL = 0 }, "ttl"},
		{"bad base_url", func(c *Config) { c.BaseURL = "api.test" }, "base_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			_, err := newProvider(cfg, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegisteredInDefault(t *testing.T) {
	params := map[string]any{"api_token": "t", "zone": "example.com", "rr_name": "@"}

	if _, err := plugin.Default.BuildProvider(Name, params); err != nil {
		t.Fatal(err)
	}

	delete(params, "api_token")
	if _, err := plugin.Default.BuildProvider(Name, params); err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Errorf("error = %v, want a missing api_token complaint", err)
	}
}
