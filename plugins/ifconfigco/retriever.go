package ifconfigco

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	familyIPv4 = "ipv4"
	familyIPv6 = "ipv6"

	// maxBody caps how much of a response is read: an address is a few dozen
	// bytes, anything longer is not what we asked for.
	maxBody = 1 << 10

	requestTimeout = 30 * time.Second
)

type retriever struct {
	endpoint string
	family   string
	client   *http.Client
}

// newRetriever validates cfg and builds the retriever. A nil client selects
// one that dials over the configured IP family; tests pass their own.
func newRetriever(cfg Config, client *http.Client) (*retriever, error) {
	family := strings.ToLower(cfg.Family)
	if family != familyIPv4 && family != familyIPv6 {
		return nil, fmt.Errorf(`family: must be "ipv4" or "ipv6", got %q`, cfg.Family)
	}

	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("base_url: %q is not an http(s) URL", cfg.BaseURL)
	}

	if client == nil {
		client = &http.Client{
			Timeout:   requestTimeout,
			Transport: familyTransport(family),
		}
	}

	return &retriever{
		endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/ip",
		family:   family,
		client:   client,
	}, nil
}

// familyTransport returns a transport that only dials over the given IP
// family, so the service sees the address of that family.
func familyTransport(family string) *http.Transport {
	network := "tcp4"
	if family == familyIPv6 {
		network = "tcp6"
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}

	return transport
}

// GetIPAddress asks the service for the public address of the configured
// family.
func (r *retriever) GetIPAddress(ctx context.Context) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := r.client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("unexpected status %s: %s", resp.Status, snippet(body))
	}

	return r.parse(body)
}

// parse turns a response body into an address of the configured family.
func (r *retriever) parse(body []byte) (netip.Addr, error) {
	text := strings.TrimSpace(string(body))

	addr, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("response is not an IP address: %s", snippet(body))
	}
	addr = addr.Unmap()

	if addr.Zone() != "" || !addr.IsGlobalUnicast() {
		return netip.Addr{}, fmt.Errorf("response %s is not a global unicast address", addr)
	}

	if want4 := r.family == familyIPv4; addr.Is4() != want4 {
		return netip.Addr{}, errors.New("response " + addr.String() + " is not an " + r.family + " address")
	}

	return addr, nil
}

// snippet renders a response body for an error message.
func snippet(body []byte) string {
	const limit = 200

	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > limit {
		text = text[:limit] + "..."
	}

	return fmt.Sprintf("%q", text)
}
