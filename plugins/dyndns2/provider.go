package dyndns2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KrimsN/dnspatch/internal/httpx"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	requestTimeout = 30 * time.Second

	// maxBody caps how much of a response is read.
	maxBody = 1 << 16
)

// failures explains the status words of the protocol that mean the update was
// not applied. A word missing here is still an error, reported as it came.
var failures = map[string]string{
	"badauth":  "the login or password was rejected",
	"!donator": "the account does not have the service needed for this update",
	"notfqdn":  "the host name is not a fully qualified domain name",
	"nohost":   "the host name does not exist or does not belong to this account",
	"numhost":  "too many host names in one update",
	"abuse":    "the account is blocked for abusing the service",
	"badagent": "the service refuses this client",
	"badsys":   "the system parameter is not valid",
	"dnserr":   "the service has a DNS error; try again later",
	"911":      "the service has a problem; try again later",
	"!yours":   "the host name belongs to another account",
}

// Defaults of the parameters that name the query parameters and the client. They
// mirror the defaults in the tags of Config, which apply only to a Config
// decoded from the configuration file.
const (
	defaultIPParam   = "myip"
	defaultIPv6Param = "ipv6"
	defaultUserAgent = "dnspatch"
)

type provider struct {
	target    *url.URL
	username  string
	password  string
	hostname  string
	ipParam   string
	ipv6Param string
	userAgent string
	client    *http.Client
}

// NewForService builds the dyndns2 provider of one particular service, the way
// a wrapper package such as nicru does: it fills in the update URL and takes
// the rest of the parameters from its own configuration. A Config assembled in
// Go has no defaults applied, so an empty parameter name or User-Agent takes
// the value the configuration file would give.
func NewForService(cfg Config) (plugin.Provider, error) {
	if cfg.IPParam == "" {
		cfg.IPParam = defaultIPParam
	}
	if cfg.IPv6Param == "" {
		cfg.IPv6Param = defaultIPv6Param
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}

	p, err := newProvider(cfg, nil)
	if err != nil {
		return nil, err
	}

	return p, nil
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

	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.Hostname)), ".")
	if hostname == "" || strings.ContainsAny(hostname, "/ ,") {
		return nil, fmt.Errorf("hostname: %q is not a single domain name", cfg.Hostname)
	}

	if cfg.IPParam == "" || cfg.IPv6Param == "" || cfg.IPParam == cfg.IPv6Param {
		return nil, errors.New("ip_param and ipv6_param must be set and differ from each other")
	}

	if client == nil {
		if client, err = httpx.NewClient(cfg.Proxy, requestTimeout); err != nil {
			return nil, err
		}
	}

	// The login travels in a header that a redirect would carry along to the
	// address the server names. A redirect is reported as the unexpected
	// status it is. The client is copied so that a caller's own is left as it
	// was.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &provider{
		target:    target,
		username:  cfg.Username,
		password:  cfg.Password,
		hostname:  hostname,
		ipParam:   cfg.IPParam,
		ipv6Param: cfg.IPv6Param,
		userAgent: cfg.UserAgent,
		client:    &noRedirect,
	}, nil
}

// Update sends the valid addresses of addrs in one request. The record type
// follows from the family: the service writes an A record for IPv4 and an AAAA
// record for IPv6.
func (p *provider) Update(ctx context.Context, addrs plugin.Addresses, _ plugin.RecordOptions) error {
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.New("no address to write")
	}

	query := p.target.Query()
	query.Set("hostname", p.hostname)
	if addrs.V4.IsValid() {
		query.Set(p.ipParam, addrs.V4.String())
	}
	if addrs.V6.IsValid() {
		query.Set(p.ipv6Param, addrs.V6.String())
	}

	target := *p.target
	target.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(p.username, p.password)
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("the login or password was rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s: %s", resp.Status, httpx.Snippet(body))
	}

	return checkAnswer(body)
}

// checkAnswer reads the status words of a response. The service answers HTTP
// 200 whatever happened, a wrong password included, so the body decides. One
// line comes back per address written, and every one of them must be a success.
func checkAnswer(body []byte) error {
	lines := strings.Split(string(body), "\n")

	seen := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		seen = true

		switch word := strings.ToLower(fields[0]); word {
		case "good", "nochg":
		default:
			if reason := failures[word]; reason != "" {
				return fmt.Errorf("%s: %s", word, reason)
			}
			return fmt.Errorf("the service did not confirm the update: %s", httpx.Snippet(body))
		}
	}

	if !seen {
		return errors.New("the service answered with an empty body")
	}

	return nil
}
