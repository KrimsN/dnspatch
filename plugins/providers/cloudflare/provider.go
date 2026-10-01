package cloudflare

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
	"strings"
	"time"

	"github.com/dnspatch/dnspatch/internal/httpx"
	"github.com/dnspatch/dnspatch/plugin"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 20

	// listLimit is passed as the API's per_page parameter when listing
	// records. The list is already narrowed to one name and type, so more
	// than this many matches means something odd is going on in the zone;
	// setOne refuses to guess rather than work from a partial list.
	listLimit = 100
)

// provider publishes an address to one record of one zone on Cloudflare.
type provider struct {
	token   string
	base    string
	zoneID  string
	zone    string
	label   string
	ttl     int
	proxied bool
	client  *http.Client
}

// record is one DNS record as the Cloudflare API represents it.
type record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
}

// writeRequest is the body of a POST or PATCH to .../dns_records(/{id}).
type writeRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
}

// apiError is one entry of the "errors" array the API includes in every
// response, successful or not.
type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// envelope is the shape every Cloudflare API v4 response shares: the payload
// travels in "result", success or failure in "success" and "errors" alongside
// it, independent of the HTTP status.
type envelope struct {
	Success    bool            `json:"success"`
	Errors     []apiError      `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

// newProvider validates cfg and builds the provider. A nil client selects one
// that honours the proxy parameter; tests pass their own.
func newProvider(cfg Config, client *http.Client) (*provider, error) {
	zoneID := strings.TrimSpace(cfg.ZoneID)
	if zoneID == "" {
		return nil, errors.New("zone_id: must not be empty")
	}

	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.Zone)), ".")
	if zone == "" || strings.ContainsAny(zone, "/ ") {
		return nil, fmt.Errorf("zone: %q is not a domain name", cfg.Zone)
	}

	label := strings.ToLower(strings.TrimSpace(cfg.RRName))
	if label == "" || strings.HasSuffix(label, ".") {
		return nil, fmt.Errorf(`rr_name: %q must be "@", "*" or a name relative to the zone, without a trailing dot`, cfg.RRName)
	}

	if cfg.TTL < 0 {
		return nil, fmt.Errorf("ttl: %d must not be negative", cfg.TTL)
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

	// The token travels in an Authorization header, not the URL, but a 307
	// or 308 would still repeat it at whatever address the server names. A
	// redirect is reported as the unexpected status it is. The client is
	// copied so a caller's own is left as it was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &noRedirect

	return &provider{
		token:   cfg.Token,
		base:    strings.TrimRight(cfg.BaseURL, "/"),
		zoneID:  zoneID,
		zone:    zone,
		label:   label,
		ttl:     cfg.TTL,
		proxied: cfg.Proxied,
		client:  client,
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

// fqdn is the fully qualified name of the configured record, the form the
// API's "name" field uses: the zone itself for the apex, otherwise the label
// joined to the zone.
func (p *provider) fqdn() string {
	if p.label == "@" {
		return p.zone
	}

	return p.label + "." + p.zone
}

// setOne points the configured record at addr: an A record for IPv4, an AAAA
// record for IPv6.
//
// It lists the records already at the configured name and type, updates the
// one matching record in place if it differs, and creates a new one
// otherwise. If more than one record of the wanted type already exists at the
// name and none of them is addr, this refuses to guess which one to replace.
func (p *provider) setOne(ctx context.Context, addr netip.Addr, ttl int) error {
	recType := "AAAA"
	if addr.Is4() {
		recType = "A"
	}

	name := p.fqdn()

	records, err := p.listRecords(ctx, recType, name)
	if err != nil {
		return err
	}

	for _, r := range records {
		if sameAddress(r.Content, addr) {
			return nil
		}
	}

	switch len(records) {
	case 0:
		return p.createRecord(ctx, recType, name, addr, ttl)
	case 1:
		return p.updateRecord(ctx, records[0].ID, recType, name, addr, records[0].TTL, records[0].Proxied)
	default:
		return fmt.Errorf("name %s has %d %s records and none is %s; refusing to guess which one to replace",
			name, len(records), recType, addr)
	}
}

// sameAddress reports whether the value of a record is addr. Content that is
// not an address is compared as text.
func sameAddress(value string, addr netip.Addr) bool {
	value = strings.TrimSpace(value)

	parsed, err := netip.ParseAddr(value)
	if err != nil {
		return value == addr.String()
	}

	return parsed.Unmap() == addr.Unmap()
}

// listRecords fetches every record of the zone matching name and recType.
// listLimit is passed as the page size; if there are more matches than that,
// the mismatch is reported rather than silently working from a partial list.
func (p *provider) listRecords(ctx context.Context, recType, name string) ([]record, error) {
	path := fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s&per_page=%d",
		url.PathEscape(p.zoneID), url.QueryEscape(recType), url.QueryEscape(name), listLimit)

	var records []record
	env, err := p.call(ctx, http.MethodGet, path, nil, &records)
	if err != nil {
		return nil, fmt.Errorf("listing records: %w", err)
	}

	if env.ResultInfo.TotalCount > len(records) {
		return nil, fmt.Errorf("name %s has %d %s records, more than the %d this provider can list in one page",
			name, env.ResultInfo.TotalCount, recType, listLimit)
	}

	return records, nil
}

// createRecord adds a new record.
func (p *provider) createRecord(ctx context.Context, recType, name string, addr netip.Addr, ttl int) error {
	body := writeRequest{Type: recType, Name: name, Content: addr.String(), TTL: ttl, Proxied: p.proxied}
	path := fmt.Sprintf("/zones/%s/dns_records", url.PathEscape(p.zoneID))

	_, err := p.call(ctx, http.MethodPost, path, body, nil)
	return err
}

// updateRecord points an existing record at addr, keeping its own TTL and
// proxied setting.
func (p *provider) updateRecord(ctx context.Context, id, recType, name string, addr netip.Addr, ttl int, proxied bool) error {
	body := writeRequest{Type: recType, Name: name, Content: addr.String(), TTL: ttl, Proxied: proxied}
	path := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(p.zoneID), url.PathEscape(id))

	_, err := p.call(ctx, http.MethodPatch, path, body, nil)
	return err
}

// call invokes one DNS API endpoint under p.base, attaching the bearer token,
// decodes the envelope every response shares and reports failure whether it
// shows up as an HTTP status or as success:false in the body.
func (p *provider) call(ctx context.Context, method, path string, body, out any) (envelope, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return envelope{}, err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.base+path, reader)
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return envelope{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return envelope{}, fmt.Errorf("reading response: %w", err)
	}

	var env envelope
	parsed := json.Unmarshal(respBody, &env) == nil

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (parsed && !env.Success) {
		if parsed {
			return envelope{}, fmt.Errorf("%s %s: %w", method, path, apiFailure(resp.StatusCode, env.Errors))
		}
		return envelope{}, fmt.Errorf("%s %s: unexpected status %d: %s", method, path, resp.StatusCode, httpx.Snippet(respBody))
	}

	if !parsed {
		return envelope{}, fmt.Errorf("%s %s: response is not JSON: %s", method, path, httpx.Snippet(respBody))
	}

	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return envelope{}, fmt.Errorf("%s %s: result is not the expected shape: %s", method, path, httpx.Snippet(env.Result))
		}
	}

	return env, nil
}

// apiFailure renders the errors array of a failed call, falling back to the
// bare status when the API reported none.
func apiFailure(status int, errs []apiError) error {
	if len(errs) == 0 {
		return fmt.Errorf("unexpected status %d", status)
	}

	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("%s (%d)", e.Message, e.Code)
	}

	return fmt.Errorf("unexpected status %d: %s", status, strings.Join(parts, "; "))
}
