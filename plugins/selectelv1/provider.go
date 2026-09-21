package selectelv1

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
	"strconv"
	"strings"
	"time"
)

const (
	requestTimeout = 30 * time.Second

	// pageSize is the page size requested when listing records; it is the
	// API's own default and maximum.
	pageSize = 1000

	// maxBody caps how much of a response is read.
	maxBody = 1 << 20
)

type provider struct {
	token  string
	base   string
	zone   string
	name   string
	ttl    int
	client *http.Client
}

// record is a resource record as the API returns it.
type record struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// recordBody is the payload for creating and updating a record.
type recordBody struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// newProvider validates cfg and builds the provider. A nil client selects a
// default one; tests pass their own.
func newProvider(cfg Config, client *http.Client) (*provider, error) {
	zone := normalizeName(cfg.Zone)
	if zone == "" || strings.ContainsAny(zone, "/ ") {
		return nil, fmt.Errorf("zone: %q is not a domain name", cfg.Zone)
	}

	label := strings.TrimSpace(cfg.RRName)
	if label == "" || strings.HasSuffix(label, ".") {
		return nil, fmt.Errorf(`rr_name: %q must be "@", "*" or a name relative to the zone, without a trailing dot`, cfg.RRName)
	}

	if cfg.TTL <= 0 {
		return nil, fmt.Errorf("ttl: must be positive, got %d", cfg.TTL)
	}

	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("base_url: %q is not an http(s) URL", cfg.BaseURL)
	}

	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	return &provider{
		token:  cfg.APIToken,
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		zone:   zone,
		name:   fqdn(zone, label),
		ttl:    cfg.TTL,
		client: client,
	}, nil
}

// fqdn builds the full record name from the zone and the configured label.
func fqdn(zone, label string) string {
	if label == "@" {
		return zone
	}

	return strings.ToLower(label) + "." + zone
}

// normalizeName lowercases a DNS name and drops the trailing dot, so that
// names compare equal however the API or the user spells them.
func normalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// SetIPAddress points the configured record at addr: an A record for IPv4, an
// AAAA record for IPv6. The record is created if it does not exist.
func (p *provider) SetIPAddress(ctx context.Context, addr netip.Addr) error {
	if !addr.IsValid() {
		return errors.New("invalid address")
	}

	recType := "AAAA"
	if addr.Is4() {
		recType = "A"
	}
	content := addr.String()

	existing, err := p.findRecords(ctx, recType)
	if err != nil {
		return err
	}

	switch len(existing) {
	case 0:
		return p.write(ctx, http.MethodPost, p.recordsPath(), recordBody{
			Type: recType, Name: p.name, Content: content, TTL: p.ttl,
		})
	case 1:
		current := existing[0]
		if current.Content == content && current.TTL == p.ttl {
			return nil
		}

		return p.write(ctx, http.MethodPut, p.recordsPath()+"/"+strconv.FormatInt(current.ID, 10), recordBody{
			Type: recType, Name: current.Name, Content: content, TTL: p.ttl,
		})
	default:
		return fmt.Errorf("zone %s has %d %s records named %s; refusing to guess which one to update",
			p.zone, len(existing), recType, p.name)
	}
}

func (p *provider) recordsPath() string {
	return "/" + url.PathEscape(p.zone) + "/records"
}

// findRecords lists the records of the given type whose name is the
// configured one. The API pages its answers, so all pages are read.
func (p *provider) findRecords(ctx context.Context, recType string) ([]record, error) {
	var found []record

	for offset := 0; ; offset += pageSize {
		query := url.Values{
			"record_types": {recType},
			"limit":        {strconv.Itoa(pageSize)},
			"offset":       {strconv.Itoa(offset)},
		}

		var page []record
		if err := p.do(ctx, http.MethodGet, p.recordsPath()+"?"+query.Encode(), nil, &page); err != nil {
			return nil, fmt.Errorf("listing records: %w", err)
		}

		for _, rec := range page {
			if rec.Type == recType && normalizeName(rec.Name) == p.name {
				found = append(found, rec)
			}
		}

		if len(page) < pageSize {
			return found, nil
		}
	}
}

// write performs a create or update request and discards the answer.
func (p *provider) write(ctx context.Context, method, path string, body recordBody) error {
	if err := p.do(ctx, method, path, body, nil); err != nil {
		verb := "updating"
		if method == http.MethodPost {
			verb = "creating"
		}

		return fmt.Errorf("%s record %s: %w", verb, p.name, err)
	}

	return nil
}

// do sends one request. A non-2xx status becomes an error carrying the body
// of the response; a 2xx body is decoded into out when out is not nil.
func (p *provider) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.base+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("X-Token", p.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp.Status, resp.StatusCode, data)
	}

	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}

	return nil
}

// statusError describes an unsuccessful response, including its body.
func statusError(status string, code int, body []byte) error {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 500 {
		text = text[:500] + "..."
	}

	msg := "unexpected status " + status
	if text != "" {
		msg += ": " + text
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		msg += " (check api_token)"
	}

	return errors.New(msg)
}
