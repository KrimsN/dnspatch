package regru

import (
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

	"github.com/KrimsN/dnspatch/internal/httpx"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 20
)

type provider struct {
	username string
	password string
	base     string
	zone     string
	label    string
	client   *http.Client
}

// resourceRecord is one entry of a zone as get_resource_records lists it.
type resourceRecord struct {
	Subname string `json:"subname"`
	Rectype string `json:"rectype"`
	Content string `json:"content"`
}

// envelope is the answer every API function returns. A call can fail as a
// whole (result and error_* at the top level) or for one domain (the same
// fields on the domain entry); HTTP status is 200 in both cases.
type envelope struct {
	Result    string `json:"result"`
	ErrorCode string `json:"error_code"`
	ErrorText string `json:"error_text"`
	Answer    struct {
		Domains []struct {
			Dname     string           `json:"dname"`
			Result    string           `json:"result"`
			ErrorCode string           `json:"error_code"`
			ErrorText string           `json:"error_text"`
			Rrs       []resourceRecord `json:"rrs"`
		} `json:"domains"`
	} `json:"answer"`
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

	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("base_url: %q is not an http(s) URL", cfg.BaseURL)
	}

	if client == nil {
		var err error
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	return &provider{
		username: cfg.Username,
		password: cfg.Password,
		base:     strings.TrimRight(cfg.BaseURL, "/"),
		zone:     zone,
		label:    label,
		client:   client,
	}, nil
}

// SetIPAddress points the configured record at addr: an A record for IPv4, an
// AAAA record for IPv6.
//
// The API has no way to change a record in place, so a differing record is
// replaced: the new one is added first and the old one removed afterwards,
// which keeps the name resolvable throughout.
func (p *provider) SetIPAddress(ctx context.Context, addr netip.Addr) error {
	if !addr.IsValid() {
		return errors.New("invalid address")
	}

	recType, addMethod := "AAAA", "zone/add_aaaa"
	if addr.Is4() {
		recType, addMethod = "A", "zone/add_alias"
	}
	content := addr.String()

	existing, err := p.findRecords(ctx, recType)
	if err != nil {
		return err
	}

	switch len(existing) {
	case 0:
		return p.call(ctx, addMethod, map[string]any{"subdomain": p.label, "ipaddr": content}, nil)
	case 1:
		if existing[0].Content == content {
			return nil
		}
	default:
		return fmt.Errorf("zone %s has %d %s records named %s; refusing to guess which one to replace",
			p.zone, len(existing), recType, p.label)
	}

	if err := p.call(ctx, addMethod, map[string]any{"subdomain": p.label, "ipaddr": content}, nil); err != nil {
		return err
	}

	return p.call(ctx, "zone/remove_record", map[string]any{
		"subdomain":   p.label,
		"record_type": recType,
		"content":     existing[0].Content,
	}, nil)
}

// findRecords lists the records of the given type at the configured name.
func (p *provider) findRecords(ctx context.Context, recType string) ([]resourceRecord, error) {
	var rrs []resourceRecord

	if err := p.call(ctx, "zone/get_resource_records", nil, &rrs); err != nil {
		return nil, fmt.Errorf("listing records: %w", err)
	}

	var found []resourceRecord
	for _, rr := range rrs {
		if rr.Rectype == recType && strings.ToLower(rr.Subname) == p.label {
			found = append(found, rr)
		}
	}

	return found, nil
}

// call invokes one API function for the configured zone. Extra holds the
// function's own parameters. When rrs is not nil it receives the records of
// the zone from the answer.
func (p *provider) call(ctx context.Context, method string, extra map[string]any, rrs *[]resourceRecord) error {
	input := map[string]any{"domains": []map[string]string{{"dname": p.zone}}}
	for key, value := range extra {
		input[key] = value
	}

	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}

	form := url.Values{
		"username":      {p.username},
		"password":      {p.password},
		"input_format":  {"json"},
		"output_format": {"json"},
		"input_data":    {string(encoded)},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %s: %s", method, resp.Status, snippet(body))
	}

	var answer envelope
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("%s: response is not JSON: %s", method, snippet(body))
	}

	if err := answer.failure(method, body); err != nil {
		return err
	}

	if rrs != nil {
		*rrs = answer.Answer.Domains[0].Rrs
	}

	return nil
}

// failure reports a failed call, whether the API rejected it as a whole or
// only for the domain. It returns nil for a successful call that answered for
// exactly one domain.
func (e envelope) failure(method string, body []byte) error {
	if e.Result != "success" {
		return apiError(method, e.ErrorCode, e.ErrorText, body)
	}

	if len(e.Answer.Domains) != 1 {
		return fmt.Errorf("%s: expected an answer for one domain, got %d: %s", method, len(e.Answer.Domains), snippet(body))
	}

	if domain := e.Answer.Domains[0]; domain.Result != "success" {
		return apiError(method, domain.ErrorCode, domain.ErrorText, body)
	}

	return nil
}

func apiError(method, code, text string, body []byte) error {
	if code == "" && text == "" {
		return fmt.Errorf("%s: call failed: %s", method, snippet(body))
	}

	return fmt.Errorf("%s: %s: %s", method, code, text)
}

// snippet renders a response body for an error message.
func snippet(body []byte) string {
	const limit = 300

	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > limit {
		text = text[:limit] + "..."
	}

	return fmt.Sprintf("%q", text)
}
