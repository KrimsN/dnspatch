package yandexcloud

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KrimsN/dnspatch/internal/httpx"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 20

	// jwtLife is the lifetime of the JWT sent to the IAM API; the service
	// accepts at most one hour.
	jwtLife = time.Hour

	// tokenSkew reissues the token this long before it actually expires, so a
	// call never starts with a token that dies mid-flight.
	tokenSkew = time.Minute

	// fallbackTokenLife is used when the IAM response carries no parseable
	// expiry: shorter than the documented 12h maximum, so a missing expiry
	// fails safe towards reauthenticating too often rather than too rarely.
	fallbackTokenLife = 11 * time.Hour

	// keyHeaderLine is the marker line the service prepends to the PEM body of
	// a private key in an authorized key file; it is not valid PEM.
	keyHeaderLine = "PLEASE DO NOT REMOVE THIS LINE!"
)

// provider publishes an address to one record of one zone in Yandex Cloud DNS.
type provider struct {
	keyID     string
	accountID string
	key       *rsa.PrivateKey
	iamURL    string
	base      string
	zoneID    string
	label     string
	ttl       int
	client    *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// recordSet is one record set as the DNS API returns it. The API encodes the
// 64-bit TTL as a JSON string, so it is read as a json.Number, which accepts
// both forms.
type recordSet struct {
	Name string      `json:"name"`
	Type string      `json:"type"`
	TTL  json.Number `json:"ttl"`
	Data []string    `json:"data"`
}

// statusError is a response with a status the caller may want to inspect.
type statusError struct {
	method, path string
	code         int
	status       string
	body         []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s %s: unexpected status %s: %s", e.method, e.path, e.status, httpx.Snippet(e.body))
}

// authorizedKey is the part of a service account authorized key file the
// provider needs.
type authorizedKey struct {
	ID               string `json:"id"`
	ServiceAccountID string `json:"service_account_id"`
	PrivateKey       string `json:"private_key"`
}

// parseKey reads an authorized key file and returns its key ID, service
// account ID and RSA private key.
func parseKey(raw string) (*authorizedKey, *rsa.PrivateKey, error) {
	var ak authorizedKey
	if err := json.Unmarshal([]byte(raw), &ak); err != nil {
		return nil, nil, errors.New("not a JSON authorized key file")
	}
	if ak.ID == "" || ak.ServiceAccountID == "" || ak.PrivateKey == "" {
		return nil, nil, errors.New("id, service_account_id and private_key must all be present")
	}

	pemText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ak.PrivateKey), keyHeaderLine))
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, nil, errors.New("private_key is not PEM")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		rsaKey, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if pkcs1Err != nil {
			return nil, nil, fmt.Errorf("private_key: %w", err)
		}
		return &ak, rsaKey, nil
	}

	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("private_key is not an RSA key")
	}

	return &ak, rsaKey, nil
}

// newProvider validates cfg and builds the provider. A nil client selects one
// that honours the proxy parameter; tests pass their own.
func newProvider(cfg Config, client *http.Client) (*provider, error) {
	ak, key, err := parseKey(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}

	zoneID := strings.TrimSpace(cfg.ZoneID)
	if zoneID == "" || strings.ContainsAny(zoneID, "/: ") {
		return nil, fmt.Errorf("zone_id: %q is not a zone ID", cfg.ZoneID)
	}

	label := strings.ToLower(strings.TrimSpace(cfg.RRName))
	if label == "" || strings.HasSuffix(label, ".") {
		return nil, fmt.Errorf(`rr_name: %q must be "@", "*" or a name relative to the zone, without a trailing dot`, cfg.RRName)
	}

	if cfg.TTL <= 0 {
		return nil, fmt.Errorf("ttl: %d must be positive", cfg.TTL)
	}

	if err = httpx.ValidateBaseURL(cfg.IAMURL); err != nil {
		return nil, fmt.Errorf("iam_url: %w", err)
	}
	if err = httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	if client == nil {
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	// The IAM call carries a signed JWT and a DNS call carries the IAM token;
	// a 307 or 308 would repeat either at whatever address the server names. A
	// redirect is reported as the unexpected status it is. The client is
	// copied so a caller's own is left as it was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &noRedirect

	return &provider{
		keyID:     ak.ID,
		accountID: ak.ServiceAccountID,
		key:       key,
		iamURL:    cfg.IAMURL,
		base:      strings.TrimRight(cfg.BaseURL, "/"),
		zoneID:    zoneID,
		label:     label,
		ttl:       cfg.TTL,
		client:    client,
	}, nil
}

// Update points the configured record(s) at addrs: an A record for V4, an
// AAAA record for V6. Each valid family is written independently; an invalid
// one is left untouched. opts.TTL, when positive, overrides the configured
// TTL for records this call creates.
func (p *provider) Update(ctx context.Context, addrs plugin.Addresses, opts plugin.RecordOptions) error {
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.New("no address to write")
	}

	ttl := p.ttl
	if opts.TTL > 0 {
		ttl = int(opts.TTL.Seconds())
	}

	name, err := p.fqdn(ctx)
	if err != nil {
		return err
	}

	var errs []error
	if addrs.V4.IsValid() {
		errs = append(errs, p.setOne(ctx, name, addrs.V4, ttl))
	}
	if addrs.V6.IsValid() {
		errs = append(errs, p.setOne(ctx, name, addrs.V6, ttl))
	}
	return errors.Join(errs...)
}

// setOne points the record set at name at addr: an A record set for IPv4, an
// AAAA one for IPv6.
//
// A record set holds every record of one name and type, and upsertRecordSets
// replaces it entirely in one call, so there is no in-between state where
// both the old and the new address are live, or neither is.
//
// If the record set already holds more than one record and none of them is
// the wanted address, this refuses to guess which one to replace.
func (p *provider) setOne(ctx context.Context, name string, addr netip.Addr, ttl int) error {
	recType := "AAAA"
	if addr.Is4() {
		recType = "A"
	}

	existing, err := p.getRecordSet(ctx, name, recType)
	if err != nil {
		return err
	}

	if existing == nil {
		return p.replace(ctx, recordSet{Name: name, Type: recType, TTL: json.Number(strconv.Itoa(ttl)), Data: []string{addr.String()}})
	}

	current := false
	for _, content := range existing.Data {
		if sameAddress(content, addr) {
			current = true
		}
	}

	if current && len(existing.Data) == 1 {
		return nil
	}

	if !current && len(existing.Data) > 1 {
		return fmt.Errorf("%s record set %s has %d records and none is %s; refusing to guess which one to replace",
			recType, name, len(existing.Data), addr)
	}

	existing.Data = []string{addr.String()}

	return p.replace(ctx, *existing)
}

// sameAddress reports whether the content of a record is addr. Content that is
// not an address is compared as text.
func sameAddress(content string, addr netip.Addr) bool {
	content = strings.TrimSpace(content)

	parsed, err := netip.ParseAddr(content)
	if err != nil {
		return content == addr.String()
	}

	return parsed.Unmap() == addr.Unmap()
}

// fqdn is the fully qualified, dot-terminated name of the configured record.
// The zone's own name is read from the API, so it need not be configured.
func (p *provider) fqdn(ctx context.Context) (string, error) {
	var zone struct {
		Zone string `json:"zone"`
	}
	if err := p.call(ctx, http.MethodGet, "/zones/"+url.PathEscape(p.zoneID), nil, &zone); err != nil {
		return "", fmt.Errorf("reading zone: %w", err)
	}

	name := strings.ToLower(zone.Zone)
	if name == "" {
		return "", fmt.Errorf("zone %s has no name in the response", p.zoneID)
	}
	if !strings.HasSuffix(name, ".") {
		name += "."
	}

	if p.label == "@" {
		return name, nil
	}

	return p.label + "." + name, nil
}

// getRecordSet looks up the record set of the given name and type. A nil
// result with no error means it does not exist yet.
func (p *provider) getRecordSet(ctx context.Context, name, recType string) (*recordSet, error) {
	path := fmt.Sprintf("/zones/%s:getRecordSet?name=%s&type=%s", url.PathEscape(p.zoneID), url.QueryEscape(name), recType)

	var rs recordSet
	err := p.call(ctx, http.MethodGet, path, nil, &rs)

	var statusErr *statusError
	if errors.As(err, &statusErr) && statusErr.code == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading record set: %w", err)
	}

	return &rs, nil
}

// replace writes rs over the record set of the same name and type, creating it
// when absent.
func (p *provider) replace(ctx context.Context, rs recordSet) error {
	body := map[string]any{"replacements": []recordSet{rs}}
	path := fmt.Sprintf("/zones/%s:upsertRecordSets", url.PathEscape(p.zoneID))

	var op struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := p.call(ctx, http.MethodPost, path, body, &op); err != nil {
		return fmt.Errorf("writing record set: %w", err)
	}

	// The call answers with a long-running operation that may already have
	// failed; one that is still pending is treated as accepted.
	if op.Error != nil {
		return fmt.Errorf("writing record set %s: %s", rs.Name, op.Error.Message)
	}

	return nil
}

// call invokes one DNS API endpoint under p.base, attaching the cached IAM
// token. A 401 is retried once after fetching a fresh token, in case the
// cached one expired or was revoked.
func (p *provider) call(ctx context.Context, method, path string, body, out any) error {
	return p.doCall(ctx, method, path, body, out, true)
}

func (p *provider) doCall(ctx context.Context, method, path string, body, out any, retryOn401 bool) error {
	token, err := p.tokenFor(ctx)
	if err != nil {
		return err
	}

	var reader io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized && retryOn401 {
		p.invalidateToken()
		return p.doCall(ctx, method, path, body, out, false)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{method: method, path: path, code: resp.StatusCode, status: resp.Status, body: respBody}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s %s: response is not JSON: %s", method, path, httpx.Snippet(respBody))
		}
	}

	return nil
}

// tokenFor returns a cached IAM token, authenticating when none is cached or
// the cached one is close to expiry.
func (p *provider) tokenFor(ctx context.Context) (string, error) {
	p.mu.Lock()
	token, expiresAt := p.token, p.expiresAt
	p.mu.Unlock()

	if token != "" && time.Now().Before(expiresAt.Add(-tokenSkew)) {
		return token, nil
	}

	return p.authenticate(ctx)
}

func (p *provider) invalidateToken() {
	p.mu.Lock()
	p.token = ""
	p.mu.Unlock()
}

// signJWT builds the PS256-signed JWT that the IAM API exchanges for a token.
func (p *provider) signJWT(now time.Time) (string, error) {
	enc := base64.RawURLEncoding

	header, err := json.Marshal(map[string]string{"typ": "JWT", "alg": "PS256", "kid": p.keyID})
	if err != nil {
		return "", err
	}

	claims, err := json.Marshal(map[string]any{
		"iss": p.accountID,
		"aud": p.iamURL,
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(jwtLife).Unix(),
	})
	if err != nil {
		return "", err
	}

	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))

	sig, err := rsa.SignPSS(rand.Reader, p.key, crypto.SHA256, sum[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	return signing + "." + enc.EncodeToString(sig), nil
}

// iamResponse is the answer of the IAM token endpoint.
type iamResponse struct {
	IAMToken  string    `json:"iamToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// authenticate exchanges a freshly signed JWT for an IAM token and caches it.
func (p *provider) authenticate(ctx context.Context) (string, error) {
	jwt, err := p.signJWT(time.Now())
	if err != nil {
		return "", err
	}

	encoded, err := json.Marshal(map[string]string{"jwt": jwt})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.iamURL, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("reading authentication response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authentication failed: unexpected status %s: %s", resp.Status, httpx.Snippet(respBody))
	}

	var parsed iamResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil || parsed.IAMToken == "" {
		return "", errors.New("authentication failed: response has no iamToken")
	}

	expiresAt := parsed.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(fallbackTokenLife)
	}

	p.mu.Lock()
	p.token, p.expiresAt = parsed.IAMToken, expiresAt
	p.mu.Unlock()

	return parsed.IAMToken, nil
}
