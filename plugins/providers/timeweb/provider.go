package timeweb

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

	// listLimit is passed as the API's limit parameter when listing records.
	// A zone with more user records than this cannot be handled correctly:
	// setOne would see only a partial list and might create a duplicate
	// instead of finding the existing one, so listRecords refuses to guess
	// and reports the mismatch instead.
	listLimit = 500
)

// provider publishes an address to one record of one zone on Timeweb Cloud.
type provider struct {
	token  string
	base   string
	zone   string
	label  string
	ttl    int
	client *http.Client
}

// recordData is the nested "data" object of a record, as the list endpoint
// represents it.
type recordData struct {
	Priority  int    `json:"priority,omitempty"`
	SubDomain string `json:"subdomain"`
	Value     string `json:"value"`
}

// record is one DNS record as the list endpoint represents it.
type record struct {
	ID   int        `json:"id"`
	Type string     `json:"type"`
	TTL  int        `json:"ttl,omitempty"`
	Data recordData `json:"data"`
}

// recordList is the answer to GET /domains/{fqdn}/dns-records.
type recordList struct {
	DNSRecords []record `json:"dns_records"`
	Meta       struct {
		Total int `json:"total"`
	} `json:"meta"`
}

// writeRequest is the body of a POST or PATCH to .../dns-records(/{id}). It
// is a separate, flat shape from record: the API does not accept the nested
// "data" object it returns on read.
type writeRequest struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	SubDomain string `json:"subdomain"`
	TTL       int    `json:"ttl,omitempty"`
}

// writeResponse is the answer to a POST or PATCH to .../dns-records(/{id}).
type writeResponse struct {
	DNSRecord record `json:"dns_record"`
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
		token:  cfg.Token,
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		zone:   zone,
		label:  label,
		ttl:    cfg.TTL,
		client: client,
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

// subdomain is the value of the API's "subdomain" field for the configured
// record name: empty for the apex, the label relative to the zone otherwise.
func (p *provider) subdomain() string {
	if p.label == "@" {
		return ""
	}

	return p.label
}

// fqdn is the full name of the configured record, used only in messages.
func (p *provider) fqdn() string {
	if p.label == "@" {
		return p.zone
	}

	return p.label + "." + p.zone
}

// setOne points the configured record at addr: an A record for IPv4, an AAAA
// record for IPv6.
//
// Timeweb Cloud models each address as its own record object, unlike
// providers whose record set holds every value of a name and type as one
// object. This lists the records already at the configured name, updates the
// one matching type in place if it exists and differs, and creates a new one
// otherwise.
//
// If more than one record of the wanted type already exists at the name and
// none of them is addr, this refuses to guess which one to replace, the same
// way it would if the provider modelled a single record set instead of
// independent records.
func (p *provider) setOne(ctx context.Context, addr netip.Addr, ttl int) error {
	recType := "AAAA"
	if addr.Is4() {
		recType = "A"
	}

	sub := p.subdomain()

	records, err := p.listRecords(ctx)
	if err != nil {
		return err
	}

	var matching []record
	for _, r := range records {
		if r.Type == recType && strings.EqualFold(r.Data.SubDomain, sub) {
			matching = append(matching, r)
		}
	}

	for _, r := range matching {
		if sameAddress(r.Data.Value, addr) {
			return nil
		}
	}

	switch len(matching) {
	case 0:
		return p.createRecord(ctx, recType, sub, addr, ttl)
	case 1:
		return p.updateRecord(ctx, matching[0].ID, recType, sub, addr, matching[0].TTL)
	default:
		return fmt.Errorf("name %s has %d %s records and none is %s; refusing to guess which one to replace",
			p.fqdn(), len(matching), recType, addr)
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

// listRecords fetches every user DNS record of the zone. listLimit is passed
// as the page size; if the zone has more records than that, the mismatch is
// reported rather than silently working from a partial list.
func (p *provider) listRecords(ctx context.Context) ([]record, error) {
	path := fmt.Sprintf("/domains/%s/dns-records?limit=%d", url.PathEscape(p.zone), listLimit)

	var list recordList
	if err := p.call(ctx, http.MethodGet, path, nil, &list); err != nil {
		return nil, fmt.Errorf("listing records: %w", err)
	}

	if list.Meta.Total > len(list.DNSRecords) {
		return nil, fmt.Errorf("zone %s has %d DNS records, more than the %d this provider can list in one page", p.zone, list.Meta.Total, listLimit)
	}

	return list.DNSRecords, nil
}

// createRecord adds a new record.
func (p *provider) createRecord(ctx context.Context, recType, sub string, addr netip.Addr, ttl int) error {
	body := writeRequest{Type: recType, Value: addr.String(), SubDomain: sub, TTL: ttl}
	path := fmt.Sprintf("/domains/%s/dns-records", url.PathEscape(p.zone))

	return p.call(ctx, http.MethodPost, path, body, nil)
}

// updateRecord points an existing record at addr, keeping its own TTL.
func (p *provider) updateRecord(ctx context.Context, id int, recType, sub string, addr netip.Addr, ttl int) error {
	body := writeRequest{Type: recType, Value: addr.String(), SubDomain: sub, TTL: ttl}
	path := fmt.Sprintf("/domains/%s/dns-records/%d", url.PathEscape(p.zone), id)

	return p.call(ctx, http.MethodPatch, path, body, nil)
}

// call invokes one DNS API endpoint under p.base, attaching the bearer token.
func (p *provider) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %w", method, path, apiError(resp.StatusCode, respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s %s: response is not JSON: %s", method, path, httpx.Snippet(respBody))
		}
	}

	return nil
}

// errorBody is the JSON shape the API answers a failed call with.
type errorBody struct {
	Message   string `json:"message"`
	ErrorCode string `json:"error_code"`
}

// apiError renders a failed call's response body, preferring the API's own
// message and error code when the body parses as one.
func apiError(status int, body []byte) error {
	var parsed errorBody
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Message != "" {
		if parsed.ErrorCode != "" {
			return fmt.Errorf("unexpected status %d: %s (%s)", status, parsed.Message, parsed.ErrorCode)
		}
		return fmt.Errorf("unexpected status %d: %s", status, parsed.Message)
	}

	return fmt.Errorf("unexpected status %d: %s", status, httpx.Snippet(body))
}
