package beget

import (
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
	"time"

	"github.com/KrimsN/dnspatch/internal/httpx"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 20

	// defaultPriority is used for a record this provider creates; the API's
	// own examples use it for a lone A/AAAA record.
	defaultPriority = 10
)

// provider publishes an address to one record of one zone on Beget.
type provider struct {
	login    string
	password string
	base     string
	zone     string
	label    string
	client   *http.Client
}

// record is one entry of a type's list, as the API represents it.
type record struct {
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
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

	if err := httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	if client == nil {
		var err error
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	// The login and password travel as URL query parameters on every call
	// (the API's own design, not this provider's choice), and a 307 or 308
	// would repeat them at whatever address the server names. A redirect is
	// reported as the unexpected status it is. The client is copied so a
	// caller's own is left as it was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &noRedirect

	return &provider{
		login:    cfg.Login,
		password: cfg.Password,
		base:     strings.TrimRight(cfg.BaseURL, "/"),
		zone:     zone,
		label:    label,
		client:   client,
	}, nil
}

// Update points the configured record(s) at addrs: an A record for V4, an
// AAAA record for V6. Each valid family is written independently; an invalid
// one is left untouched. The API has no per-record TTL, so opts.TTL is
// ignored.
func (p *provider) Update(ctx context.Context, addrs plugin.Addresses, _ plugin.RecordOptions) error {
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.New("no address to write")
	}

	var errs []error
	if addrs.V4.IsValid() {
		errs = append(errs, p.setOne(ctx, addrs.V4))
	}
	if addrs.V6.IsValid() {
		errs = append(errs, p.setOne(ctx, addrs.V6))
	}
	return errors.Join(errs...)
}

// fqdn is the name whose full record set dns/getData and dns/changeRecords
// operate on.
func (p *provider) fqdn() string {
	if p.label == "@" {
		return p.zone
	}

	return p.label + "." + p.zone
}

// setOne points the configured record at addr: an A record for IPv4, an AAAA
// record for IPv6.
//
// dns/changeRecords replaces the entire record set of the name in one call,
// covering every record type at once, not just the one being written (a
// documented footgun: sending only the changed type deletes the rest). This
// fetches the current set first and changes only the "A" or "AAAA" entry of
// it, passing every other type back exactly as received.
//
// If the record of the wanted type already holds more than one value and
// none of them is addr, this refuses to guess which one to replace, the same
// way it would if the provider modelled separate records instead of a list.
func (p *provider) setOne(ctx context.Context, addr netip.Addr) error {
	recType := "AAAA"
	if addr.Is4() {
		recType = "A"
	}

	all, err := p.getRecords(ctx)
	if err != nil {
		return err
	}

	var existing []record
	if raw, ok := all[recType]; ok {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("dns/getData: %s records: not a record list: %w", recType, err)
		}
	}

	priority := defaultPriority
	current := false
	var stale []string
	for _, rec := range existing {
		switch {
		case sameAddress(rec.Value, addr):
			current = true
			priority = rec.Priority
		case !slices.Contains(stale, rec.Value):
			stale = append(stale, rec.Value)
		}
	}

	if !current && len(existing) > 1 {
		return fmt.Errorf("name %s has %d %s records and none is %s; refusing to guess which one to replace",
			p.fqdn(), len(existing), recType, addr)
	}

	if current && len(stale) == 0 {
		return nil
	}

	encoded, err := json.Marshal([]record{{Value: addr.String(), Priority: priority}})
	if err != nil {
		return err
	}

	updated := make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		updated[k] = v
	}
	updated[recType] = encoded

	return p.changeRecords(ctx, updated)
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

// getDataResult is the answer.result of dns/getData.
type getDataResult struct {
	Records map[string]json.RawMessage `json:"records"`
}

// getRecords fetches the full, per-type record set of the configured name.
func (p *provider) getRecords(ctx context.Context) (map[string]json.RawMessage, error) {
	input, err := json.Marshal(map[string]string{"fqdn": p.fqdn()})
	if err != nil {
		return nil, err
	}

	result, err := p.call(ctx, "dns/getData", input)
	if err != nil {
		return nil, fmt.Errorf("dns/getData: %w", err)
	}

	var parsed getDataResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("dns/getData: response is not a record set: %s", httpx.Snippet(result))
	}

	if parsed.Records == nil {
		parsed.Records = map[string]json.RawMessage{}
	}

	return parsed.Records, nil
}

// changeRecords writes back the full record set of the configured name. It
// must carry every type already there, not only the one being changed: see
// setOne.
func (p *provider) changeRecords(ctx context.Context, records map[string]json.RawMessage) error {
	input, err := json.Marshal(map[string]any{"fqdn": p.fqdn(), "records": records})
	if err != nil {
		return err
	}

	if _, err := p.call(ctx, "dns/changeRecords", input); err != nil {
		return fmt.Errorf("dns/changeRecords: %w", err)
	}

	return nil
}

// envelope is the JSON structure every Beget API call answers with. A call
// can fail before reaching the method (the top-level status/error_* fields)
// or while running it (the same fields inside answer).
type envelope struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	ErrorText string `json:"error_text"`
	Answer    struct {
		Status    string          `json:"status"`
		Result    json.RawMessage `json:"result"`
		ErrorCode string          `json:"error_code"`
		ErrorText string          `json:"error_text"`
	} `json:"answer"`
}

// call invokes one API method and returns its answer.result.
//
// The login and password are sent as URL query parameters, the API's own
// design. That means the client's own transport errors (a failed dial, a
// cancelled context) carry the request URL, password and all: every error
// this returns has the password redacted before it reaches the caller.
func (p *provider) call(ctx context.Context, method string, input []byte) (result json.RawMessage, err error) {
	defer func() {
		if err != nil {
			err = redact(err, p.password)
		}
	}()

	q := url.Values{
		"login":         {p.login},
		"passwd":        {p.password},
		"input_format":  {"json"},
		"output_format": {"json"},
		"input_data":    {string(input)},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/"+method+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %s: %s", method, resp.Status, httpx.Snippet(body))
	}

	var answer envelope
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, fmt.Errorf("%s: response is not JSON: %s", method, httpx.Snippet(body))
	}

	if err := answer.failure(method, body); err != nil {
		return nil, err
	}

	return answer.Answer.Result, nil
}

// failure reports a failed call, whether the API rejected it before running
// the method or while running it. It returns nil for a successful call.
func (e envelope) failure(method string, body []byte) error {
	if e.Status != "success" {
		return apiError(method, e.ErrorCode, e.ErrorText, body)
	}

	if e.Answer.Status != "" && e.Answer.Status != "success" {
		return apiError(method, e.Answer.ErrorCode, e.Answer.ErrorText, body)
	}

	return nil
}

func apiError(method, code, text string, body []byte) error {
	if code == "" && text == "" {
		return fmt.Errorf("%s: call failed: %s", method, httpx.Snippet(body))
	}

	return fmt.Errorf("%s: %s: %s", method, code, text)
}

// redact replaces every occurrence of secret in err's message, in both its
// raw and URL-query-encoded form, so a redirected or wrapped error can never
// carry the password.
func redact(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}

	msg := err.Error()
	redacted := strings.ReplaceAll(msg, secret, "[redacted]")
	redacted = strings.ReplaceAll(redacted, url.QueryEscape(secret), "[redacted]")
	if redacted == msg {
		return err
	}

	return errors.New(redacted)
}
