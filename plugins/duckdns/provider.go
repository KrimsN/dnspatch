package duckdns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dnspatch/dnspatch/internal/httpx"
	"github.com/dnspatch/dnspatch/plugin"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 12
)

// provider publishes an address to one domain registered at DuckDNS.
type provider struct {
	target *url.URL
	domain string
	token  string
	client *http.Client
}

// newProvider validates cfg and builds the provider. A nil client selects one
// that honours the proxy parameter; tests pass their own.
func newProvider(cfg Config, client *http.Client) (*provider, error) {
	if err := httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	target, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, errors.New("base_url: not an http(s) URL")
	}

	domain := strings.ToLower(strings.TrimSpace(cfg.Domain))
	if domain == "" || strings.ContainsAny(domain, "/ ,.") {
		return nil, fmt.Errorf("domain: %q must be the subdomain alone, without the .duckdns.org suffix", cfg.Domain)
	}

	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("token is required")
	}

	if client == nil {
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	// The token travels in the query string, which a redirect would repeat at
	// whatever address the server names. A redirect is reported as the
	// unexpected status it is. The client is copied so a caller's own is left
	// as it was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &provider{
		target: target,
		domain: domain,
		token:  cfg.Token,
		client: &noRedirect,
	}, nil
}

// Update sends the valid addresses of addrs in one request. DuckDNS updates
// the A record from ip and the AAAA record from ipv6 independently, so each
// parameter is sent only for a family addrs reports; an invalid field must
// never reach the query as an empty parameter, which DuckDNS reads as "use
// the address of this request" rather than "leave this family alone".
func (p *provider) Update(ctx context.Context, addrs plugin.Addresses, _ plugin.RecordOptions) error {
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.New("no address to write")
	}

	query := p.target.Query()
	query.Set("domains", p.domain)
	query.Set("token", p.token)
	if addrs.V4.IsValid() {
		query.Set("ip", addrs.V4.String())
	}
	if addrs.V6.IsValid() {
		query.Set("ipv6", addrs.V6.String())
	}

	target := *p.target
	target.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return sanitizeError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s: %s", resp.Status, httpx.Snippet(body))
	}

	return checkAnswer(body)
}

// checkAnswer reads the response. DuckDNS answers OK or KO with HTTP 200
// either way, a wrong domain or token included, so the body decides.
func checkAnswer(body []byte) error {
	switch strings.TrimSpace(string(body)) {
	case "OK":
		return nil
	case "KO":
		return errors.New("the service rejected the domain or token")
	default:
		return fmt.Errorf("the service did not confirm the update: %s", httpx.Snippet(body))
	}
}

// sanitizeError strips the query string, which carries the token, out of an
// error that names the request URL: net/http reports a failed request as a
// *url.Error whose Error() repeats the full URL, and that must not put the
// token in a log.
func sanitizeError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}

	target, parseErr := url.Parse(uerr.URL)
	if parseErr != nil {
		return fmt.Errorf("%s <redacted>: %w", uerr.Op, uerr.Err)
	}
	if target.RawQuery != "" {
		target.RawQuery = "REDACTED"
	}

	return fmt.Errorf("%s %s: %w", uerr.Op, target, uerr.Err)
}
