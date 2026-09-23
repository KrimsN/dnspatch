package yandexcloud

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	testKeyID     = "key-1"
	testAccountID = "sa-1"
	testZoneID    = "zone-1"
	testZoneName  = "example.com."
)

// testKey is generated once: RSA key generation is slow.
var (
	testKeyOnce sync.Once
	testKeyRSA  *rsa.PrivateKey
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	testKeyOnce.Do(func() {
		var err error
		testKeyRSA, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
	})

	return testKeyRSA
}

// keyFile renders key the way the service writes an authorized key file.
func keyFile(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	pemText := "PLEASE DO NOT REMOVE THIS LINE!\n" + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	out, err := json.Marshal(map[string]string{"id": testKeyID, "service_account_id": testAccountID, "private_key": pemText})
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// fakeAPI is an in-memory stand-in for the IAM endpoint and the DNS API.
type fakeAPI struct {
	t   *testing.T
	pub *rsa.PublicKey

	mu        sync.Mutex
	sets      map[string]recordSet // key: name+"/"+type
	authCalls int
	upserts   int
	token     string
	expiresIn time.Duration

	// rejectOnce makes the next DNS call answer 401.
	rejectOnce bool
	// opError makes upsertRecordSets answer with a failed operation.
	opError string
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()

	api := &fakeAPI{t: t, pub: &testKey(t).PublicKey, sets: map[string]recordSet{}, token: "t1.first", expiresIn: 12 * time.Hour}

	mux := http.NewServeMux()
	mux.HandleFunc("/iam/v1/tokens", api.handleIAM)
	mux.HandleFunc("/dns/v1/zones/", api.handleDNS)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return api, srv
}

func (a *fakeAPI) handleIAM(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.authCalls++

	var body struct {
		JWT string `json:"jwt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	if err := a.verifyJWT(body.JWT); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	a.token = "t1.token-" + string(rune('a'+a.authCalls))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"iamToken":  a.token,
		"expiresAt": time.Now().Add(a.expiresIn).UTC().Format(time.RFC3339),
	})
}

func (a *fakeAPI) verifyJWT(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errString("jwt does not have three parts")
	}

	enc := base64.RawURLEncoding

	headerRaw, _ := enc.DecodeString(parts[0])
	var header map[string]string
	if err := json.Unmarshal(headerRaw, &header); err != nil || header["alg"] != "PS256" || header["kid"] != testKeyID || header["typ"] != "JWT" {
		return errString("bad jwt header " + string(headerRaw))
	}

	claimsRaw, _ := enc.DecodeString(parts[1])
	var claims struct {
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil || claims.Iss != testAccountID || claims.Aud == "" || claims.Exp <= time.Now().Unix() {
		return errString("bad jwt claims " + string(claimsRaw))
	}

	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return err
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	return rsa.VerifyPSS(a.pub, crypto.SHA256, sum[:], sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
}

type errString string

func (e errString) Error() string { return string(e) }

func (a *fakeAPI) handleDNS(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.rejectOnce {
		a.rejectOnce = false
		http.Error(w, "expired", http.StatusUnauthorized)
		return
	}

	if r.Header.Get("Authorization") != "Bearer "+a.token {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/dns/v1/zones/")
	id, action, _ := strings.Cut(rest, ":")
	if id != testZoneID {
		http.Error(w, "no such zone", http.StatusNotFound)
		return
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]string{"id": testZoneID, "zone": testZoneName})
	case action == "getRecordSet" && r.Method == http.MethodGet:
		key := r.URL.Query().Get("name") + "/" + r.URL.Query().Get("type")
		rs, ok := a.sets[key]
		if !ok {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": rs.Name, "type": rs.Type, "ttl": rs.TTL.String(), "data": rs.Data})
	case action == "upsertRecordSets" && r.Method == http.MethodPost:
		a.upserts++

		var body struct {
			Replacements []recordSet `json:"replacements"`
			Merges       []recordSet `json:"merges"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Merges) > 0 {
			http.Error(w, "unexpected body", http.StatusBadRequest)
			return
		}
		if a.opError != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"done": true, "error": map[string]string{"message": a.opError}})
			return
		}
		for _, rs := range body.Replacements {
			a.sets[rs.Name+"/"+rs.Type] = rs
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"done": false})
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
	}
}

func testConfig(t *testing.T, srv *httptest.Server) Config {
	t.Helper()

	return Config{
		Key:     keyFile(t, testKey(t)),
		ZoneID:  testZoneID,
		RRName:  "home",
		TTL:     300,
		IAMURL:  srv.URL + "/iam/v1/tokens",
		BaseURL: srv.URL + "/dns/v1",
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

func v4(s string) plugin.Addresses { return plugin.Addresses{V4: netip.MustParseAddr(s)} }

func TestCreatesMissingRecord(t *testing.T) {
	api, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	got, ok := api.sets["home.example.com./A"]
	if !ok {
		t.Fatalf("record not created: %+v", api.sets)
	}
	if got.TTL.String() != "300" || len(got.Data) != 1 || got.Data[0] != "203.0.113.5" {
		t.Fatalf("unexpected record %+v", got)
	}
}

func TestOptionsTTLOverridesConfigured(t *testing.T) {
	api, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{TTL: 90 * time.Second}); err != nil {
		t.Fatal(err)
	}

	if got := api.sets["home.example.com./A"].TTL.String(); got != "90" {
		t.Fatalf("ttl = %s, want 90", got)
	}
}

func TestApexAndBothFamilies(t *testing.T) {
	api, srv := newFakeAPI(t)
	cfg := testConfig(t, srv)
	cfg.RRName = "@"
	p := mustProvider(t, cfg, srv)

	addrs := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.5"), V6: netip.MustParseAddr("2001:db8::5")}
	if err := p.Update(context.Background(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := api.sets["example.com./A"].Data; len(got) != 1 || got[0] != "203.0.113.5" {
		t.Fatalf("A = %v", got)
	}
	if got := api.sets["example.com./AAAA"].Data; len(got) != 1 || got[0] != "2001:db8::5" {
		t.Fatalf("AAAA = %v", got)
	}
}

func TestReplacesStaleAddressKeepingTTL(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.sets["home.example.com./A"] = recordSet{Name: "home.example.com.", Type: "A", TTL: "3600", Data: []string{"198.51.100.1"}}
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	got := api.sets["home.example.com./A"]
	if got.TTL.String() != "3600" || len(got.Data) != 1 || got.Data[0] != "203.0.113.5" {
		t.Fatalf("unexpected record %+v", got)
	}
}

func TestUnchangedAddressWritesNothing(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.sets["home.example.com./A"] = recordSet{Name: "home.example.com.", Type: "A", TTL: "300", Data: []string{"203.0.113.5"}}
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if api.upserts != 0 {
		t.Fatalf("%d writes for an unchanged address", api.upserts)
	}
}

func TestUntouchedFamilyIsLeftAlone(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.sets["home.example.com./AAAA"] = recordSet{Name: "home.example.com.", Type: "AAAA", TTL: "300", Data: []string{"2001:db8::1"}}
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := api.sets["home.example.com./AAAA"].Data; len(got) != 1 || got[0] != "2001:db8::1" {
		t.Fatalf("AAAA changed: %v", got)
	}
}

func TestRefusesToGuessAmongSeveralRecords(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.sets["home.example.com./A"] = recordSet{Name: "home.example.com.", Type: "A", TTL: "300", Data: []string{"198.51.100.1", "198.51.100.2"}}
	p := mustProvider(t, testConfig(t, srv), srv)

	err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("err = %v", err)
	}
	if api.upserts != 0 {
		t.Fatal("record set was written")
	}
}

func TestNoAddressIsAnError(t *testing.T) {
	_, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), plugin.Addresses{}, plugin.RecordOptions{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestTokenIsCachedAcrossCalls(t *testing.T) {
	api, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(t, srv), srv)

	for _, ip := range []string{"203.0.113.5", "203.0.113.6"} {
		if err := p.Update(context.Background(), v4(ip), plugin.RecordOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	if api.authCalls != 1 {
		t.Fatalf("authenticated %d times, want 1", api.authCalls)
	}
}

func TestExpiringTokenIsRenewed(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.expiresIn = 30 * time.Second // inside tokenSkew: already stale
	p := mustProvider(t, testConfig(t, srv), srv)

	for _, ip := range []string{"203.0.113.5", "203.0.113.6"} {
		if err := p.Update(context.Background(), v4(ip), plugin.RecordOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	if api.authCalls < 2 {
		t.Fatalf("authenticated %d times, want a renewal", api.authCalls)
	}
}

func TestUnauthorizedTriggersOneReauthentication(t *testing.T) {
	api, srv := newFakeAPI(t)
	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	api.mu.Lock()
	api.rejectOnce = true
	api.mu.Unlock()

	if err := p.Update(context.Background(), v4("203.0.113.6"), plugin.RecordOptions{}); err != nil {
		t.Fatal(err)
	}

	if api.authCalls != 2 {
		t.Fatalf("authenticated %d times, want 2", api.authCalls)
	}
}

func TestFailedOperationIsReported(t *testing.T) {
	api, srv := newFakeAPI(t)
	api.opError = "quota exceeded"
	p := mustProvider(t, testConfig(t, srv), srv)

	err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestIAMRejectionIsReported(t *testing.T) {
	api, srv := newFakeAPI(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	api.pub = &other.PublicKey // signature will not verify
	p := mustProvider(t, testConfig(t, srv), srv)

	err = p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownZoneIsReported(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(t, srv)
	cfg.ZoneID = "missing"
	p := mustProvider(t, cfg, srv)

	err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "reading zone") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("redirect target was contacted")
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	p := mustProvider(t, testConfig(t, srv), srv)

	if err := p.Update(context.Background(), v4("203.0.113.5"), plugin.RecordOptions{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestConfigValidation(t *testing.T) {
	_, srv := newFakeAPI(t)
	good := testConfig(t, srv)

	tests := map[string]func(*Config){
		"key not json":      func(c *Config) { c.Key = "nope" },
		"key without pem":   func(c *Config) { c.Key = `{"id":"a","service_account_id":"b","private_key":"x"}` },
		"key missing id":    func(c *Config) { c.Key = `{"service_account_id":"b","private_key":"x"}` },
		"zone id empty":     func(c *Config) { c.ZoneID = "" },
		"zone id has slash": func(c *Config) { c.ZoneID = "a/b" },
		"rr name empty":     func(c *Config) { c.RRName = "" },
		"rr name dotted":    func(c *Config) { c.RRName = "home." },
		"ttl zero":          func(c *Config) { c.TTL = 0 },
		"iam url bad":       func(c *Config) { c.IAMURL = "ftp://x" },
		"base url bad":      func(c *Config) { c.BaseURL = "" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := good
			mutate(&cfg)

			if _, err := newProvider(cfg, srv.Client()); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
