package selectel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
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

	// tokenSkew reissues the token this long before it actually expires, so a
	// call never starts with a token that dies mid-flight.
	tokenSkew = time.Minute

	// fallbackTokenLife is used when the identity service's response does not
	// carry a parseable expiry: shorter than the documented 24h lifetime, so a
	// missing expiry fails safe towards reauthenticating too often rather than
	// too rarely.
	fallbackTokenLife = 23 * time.Hour

	minTTL = 60
	maxTTL = 604800
)

// provider publishes an address to one record of one zone in Selectel DNS
// Hosting.
type provider struct {
	accountID   string
	username    string
	password    string
	projectName string
	authURL     string
	base        string
	zone        string
	label       string
	ttl         int
	client      *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// record is one entry of an rrset's content.
type record struct {
	Content  string `json:"content"`
	Disabled bool   `json:"disabled,omitempty"`
}

// rrset is one record set as the DNS API returns it.
type rrset struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	TTL     int      `json:"ttl"`
	Type    string   `json:"type"`
	Records []record `json:"records"`
}

// newProvider validates cfg and builds the provider. A nil client selects one
// that honours the proxy parameter; tests pass their own.
func newProvider(cfg Config, client *http.Client) (*provider, error) {
	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.Zone)), ".")
	if zone == "" || strings.ContainsAny(zone, "/ ") {
		return nil, fmt.Errorf("zone: %q is not a domain name", cfg.Zone)
	}

	label := strings.ToLower(strings.TrimSpace(cfg.RRName))
	if label == "" || strings.HasSuffix(label, ".") {
		return nil, fmt.Errorf(`rr_name: %q must be "@", "*" or a name relative to the zone, without a trailing dot`, cfg.RRName)
	}

	if cfg.TTL < minTTL || cfg.TTL > maxTTL {
		return nil, fmt.Errorf("ttl: %d must be between %d and %d", cfg.TTL, minTTL, maxTTL)
	}

	if err := httpx.ValidateBaseURL(cfg.AuthURL); err != nil {
		return nil, fmt.Errorf("auth_url: %w", err)
	}
	if err := httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	if client == nil {
		var err error
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	// Every call to the identity service carries the account password, and a
	// call to the DNS API carries the IAM token; a 307 or 308 would repeat
	// either at whatever address the server names. A redirect is reported as
	// the unexpected status it is. The client is copied so a caller's own is
	// left as it was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &noRedirect

	return &provider{
		accountID:   cfg.AccountID,
		username:    cfg.Username,
		password:    cfg.Password,
		projectName: cfg.ProjectName,
		authURL:     strings.TrimRight(cfg.AuthURL, "/"),
		base:        strings.TrimRight(cfg.BaseURL, "/"),
		zone:        zone,
		label:       label,
		ttl:         cfg.TTL,
		client:      client,
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

	var errs []error
	if addrs.V4.IsValid() {
		errs = append(errs, p.setOne(ctx, addrs.V4, ttl))
	}
	if addrs.V6.IsValid() {
		errs = append(errs, p.setOne(ctx, addrs.V6, ttl))
	}
	return errors.Join(errs...)
}

// setOne points the configured record at addr: an A record for IPv4, an AAAA
// record for IPv6.
//
// A record set in Selectel DNS Hosting holds every record of one name and
// type as a single object, so replacing its content is one atomic call: there
// is no in-between state where both the old and the new address are live, and
// none where neither is.
//
// If the record set already holds more than one record and none of them is
// the wanted address, this refuses to guess which one to replace, the same
// way it would if two separate records existed on a provider that models them
// that way.
func (p *provider) setOne(ctx context.Context, addr netip.Addr, ttl int) error {
	recType := "AAAA"
	if addr.Is4() {
		recType = "A"
	}

	zoneID, err := p.findZoneID(ctx)
	if err != nil {
		return err
	}

	existing, err := p.findRRSet(ctx, zoneID, recType)
	if err != nil {
		return err
	}

	if existing == nil {
		return p.createRRSet(ctx, zoneID, recType, addr, ttl)
	}

	current := false
	var stale []string
	for _, rec := range existing.Records {
		switch {
		case sameAddress(rec.Content, addr):
			current = true
		case !slices.Contains(stale, rec.Content):
			stale = append(stale, rec.Content)
		}
	}

	if !current && len(existing.Records) > 1 {
		return fmt.Errorf("zone %s has a %s record set named %s with %d records and none is %s; refusing to guess which one to replace",
			p.zone, recType, existing.Name, len(existing.Records), addr)
	}

	if current && len(stale) == 0 {
		return nil
	}

	return p.replaceRRSet(ctx, zoneID, existing, addr)
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

// fqdn is the fully qualified, dot-terminated name of the configured record,
// the form the DNS API's rrset name uses.
func (p *provider) fqdn() string {
	if p.label == "@" {
		return p.zone + "."
	}

	return p.label + "." + p.zone + "."
}

// zoneList is the answer to GET /zones.
type zoneList struct {
	Result []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
}

// findZoneID looks up the zone by name. The filter parameter matches
// substrings, so the result is still checked for an exact name.
func (p *provider) findZoneID(ctx context.Context) (string, error) {
	var list zoneList
	path := "/zones?filter=" + url.QueryEscape(p.zone)
	if err := p.call(ctx, http.MethodGet, path, nil, &list); err != nil {
		return "", fmt.Errorf("listing zones: %w", err)
	}

	for _, z := range list.Result {
		if strings.EqualFold(strings.TrimSuffix(z.Name, "."), p.zone) {
			return z.ID, nil
		}
	}

	return "", fmt.Errorf("zone %q not found in project %q", p.zone, p.projectName)
}

// rrsetList is the answer to GET /zones/{zone_id}/rrset.
type rrsetList struct {
	Result []rrset `json:"result"`
}

// findRRSet looks up the record set of the given type at the configured name.
// A nil result with no error means it does not exist yet.
func (p *provider) findRRSet(ctx context.Context, zoneID, recType string) (*rrset, error) {
	var list rrsetList
	path := fmt.Sprintf("/zones/%s/rrset?name=%s&rrset_types=%s", url.PathEscape(zoneID), url.QueryEscape(p.fqdn()), recType)
	if err := p.call(ctx, http.MethodGet, path, nil, &list); err != nil {
		return nil, fmt.Errorf("listing records: %w", err)
	}

	switch len(list.Result) {
	case 0:
		return nil, nil
	case 1:
		return &list.Result[0], nil
	default:
		return nil, fmt.Errorf("zone %s has %d %s record sets named %s, expected at most one", p.zone, len(list.Result), recType, p.fqdn())
	}
}

// createRRSet adds a new record set holding a single record.
func (p *provider) createRRSet(ctx context.Context, zoneID, recType string, addr netip.Addr, ttl int) error {
	body := map[string]any{
		"name":    p.fqdn(),
		"ttl":     ttl,
		"type":    recType,
		"records": []record{{Content: addr.String()}},
	}

	path := fmt.Sprintf("/zones/%s/rrset", url.PathEscape(zoneID))

	return p.call(ctx, http.MethodPost, path, body, nil)
}

// replaceRRSet points an existing record set at addr and only addr, keeping
// its current TTL.
func (p *provider) replaceRRSet(ctx context.Context, zoneID string, existing *rrset, addr netip.Addr) error {
	body := map[string]any{
		"ttl":     existing.TTL,
		"records": []record{{Content: addr.String()}},
	}

	path := fmt.Sprintf("/zones/%s/rrset/%s", url.PathEscape(zoneID), url.PathEscape(existing.ID))

	return p.call(ctx, http.MethodPatch, path, body, nil)
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
	req.Header.Set("X-Auth-Token", token)
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
		return fmt.Errorf("%s %s: unexpected status %s: %s", method, path, resp.Status, httpx.Snippet(respBody))
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

// authResponse is the body Keystone returns alongside the X-Subject-Token
// header.
type authResponse struct {
	Token struct {
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"token"`
}

// authenticate obtains a project-scoped IAM token from the identity service
// and caches it.
func (p *provider) authenticate(ctx context.Context) (string, error) {
	body := map[string]any{
		"auth": map[string]any{
			"identity": map[string]any{
				"methods": []string{"password"},
				"password": map[string]any{
					"user": map[string]any{
						"name":     p.username,
						"domain":   map[string]any{"name": p.accountID},
						"password": p.password,
					},
				},
			},
			"scope": map[string]any{
				"project": map[string]any{
					"name":   p.projectName,
					"domain": map[string]any{"name": p.accountID},
				},
			},
		},
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.authURL+"/auth/tokens", bytes.NewReader(encoded))
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

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("authentication failed: unexpected status %s: %s", resp.Status, httpx.Snippet(respBody))
	}

	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", errors.New("authentication failed: response has no X-Subject-Token header")
	}

	expiresAt := time.Now().Add(fallbackTokenLife)
	var parsed authResponse
	if err := json.Unmarshal(respBody, &parsed); err == nil && !parsed.Token.ExpiresAt.IsZero() {
		expiresAt = parsed.Token.ExpiresAt
	}

	p.mu.Lock()
	p.token, p.expiresAt = token, expiresAt
	p.mu.Unlock()

	return token, nil
}
