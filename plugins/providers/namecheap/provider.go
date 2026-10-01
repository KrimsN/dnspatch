package namecheap

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dnspatch/dnspatch/httpx"
	"github.com/dnspatch/dnspatch/plugin"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 12
)

// provider publishes an address to one host at Namecheap Dynamic DNS.
type provider struct {
	target   *url.URL
	host     string
	domain   string
	password string
	client   *http.Client
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

	host := strings.TrimSpace(cfg.Host)
	if host == "" || strings.ContainsAny(host, "/ ,") {
		return nil, fmt.Errorf("host: %q must be @ or a single host record name", cfg.Host)
	}

	// The domain is not normalized: Namecheap's own docs say the case must
	// match the account exactly.
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" || strings.ContainsAny(domain, "/ ,") {
		return nil, fmt.Errorf("domain: %q must be the registered domain alone", cfg.Domain)
	}

	if strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("password is required")
	}

	if client == nil {
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	// The password travels in the query string, which a redirect would
	// repeat at whatever address the server names. A redirect is reported as
	// the unexpected status it is. The client is copied so a caller's own is
	// left as it was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &provider{
		target:   target,
		host:     host,
		domain:   domain,
		password: cfg.Password,
		client:   &noRedirect,
	}, nil
}

// Update writes addrs.V4 as the host's A record. Namecheap Dynamic DNS has no
// AAAA support: a V6-only request is an error, and a request carrying both
// families still writes V4 but reports V6 as not written.
func (p *provider) Update(ctx context.Context, addrs plugin.Addresses, _ plugin.RecordOptions) error {
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.New("no address to write")
	}

	var errs []error
	if addrs.V4.IsValid() {
		errs = append(errs, p.setV4(ctx, addrs.V4))
	}
	if addrs.V6.IsValid() {
		errs = append(errs, errors.New("namecheap dynamic DNS does not support AAAA records; the IPv6 address was not written"))
	}

	return errors.Join(errs...)
}

func (p *provider) setV4(ctx context.Context, addr netip.Addr) error {
	query := p.target.Query()
	query.Set("host", p.host)
	query.Set("domain", p.domain)
	query.Set("password", p.password)
	query.Set("ip", addr.String())

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

// answer is the XML document the API returns, both on success and on error.
type answer struct {
	XMLName  xml.Name `xml:"interface-response"`
	ErrCount int      `xml:"ErrCount"`
	Errors   struct {
		Inner string `xml:",innerxml"`
	} `xml:"errors"`
}

var xmlTag = regexp.MustCompile(`<[^>]*>`)

// checkAnswer reads the response. A well-formed answer with ErrCount 0 is a
// success; ErrCount above 0 carries the reason inside the errors element as
// one or more numbered tags (Err1, Err2, ...), which are stripped down to
// their text for the error message.
//
// The exact wording of the errors element has not been verified against a
// live account; this follows Namecheap's published documentation.
func checkAnswer(body []byte) error {
	var a answer
	if err := xml.Unmarshal(body, &a); err != nil {
		return fmt.Errorf("the service did not confirm the update: %s", httpx.Snippet(body))
	}

	if a.ErrCount == 0 {
		return nil
	}

	reason := strings.TrimSpace(xmlTag.ReplaceAllString(a.Errors.Inner, " "))
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return fmt.Errorf("the service rejected the update: %s", httpx.Snippet(body))
	}

	return fmt.Errorf("the service rejected the update: %s", reason)
}

// sanitizeError strips the query string, which carries the password, out of
// an error that names the request URL: net/http reports a failed request as
// a *url.Error whose Error() repeats the full URL, and that must not put the
// password in a log.
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
