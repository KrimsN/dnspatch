package identme

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/KrimsN/dnspatch/internal/httpx"
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

	if err := httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	if client == nil {
		var err error
		if client, err = newClient(cfg.Proxy, family); err != nil {
			return nil, err
		}
	}

	return &retriever{
		endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/",
		family:   family,
		client:   client,
	}, nil
}

// newClient builds the client the retriever uses. Without a proxy, that is
// direct or empty, the connection is pinned to the IP family. With one, the
// connection to the proxy is left alone, since the family that matters is the
// one the proxy connects to the service over, and the reply check in parse is
// what enforces the family.
func newClient(proxy, family string) (*http.Client, error) {
	if strings.TrimSpace(proxy) == "" || httpx.IsDirect(proxy) {
		return &http.Client{Timeout: requestTimeout, Transport: familyTransport(family)}, nil
	}

	return httpx.NewClient(proxy, requestTimeout)
}

// familyTransport returns a transport that only dials over the given IP
// family, so the service sees the address of that family. It never uses a
// proxy, not even the one named in HTTP_PROXY or HTTPS_PROXY: a proxy would
// make the service report the address of the proxy instead of ours.
func familyTransport(family string) *http.Transport {
	network := "tcp4"
	if family == familyIPv6 {
		network = "tcp6"
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	transport := httpx.NewTransport()
	transport.Proxy = nil
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
		return netip.Addr{}, fmt.Errorf("unexpected status %s: %s", resp.Status, httpx.Snippet(body))
	}

	return r.parse(body)
}

// parse turns a response body into an address of the configured family.
func (r *retriever) parse(body []byte) (netip.Addr, error) {
	text := strings.TrimSpace(string(body))

	addr, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("response is not an IP address: %s", httpx.Snippet(body))
	}
	addr = addr.Unmap()

	if addr.Zone() != "" || !addr.IsGlobalUnicast() {
		return netip.Addr{}, fmt.Errorf("response %s is not a global unicast address", addr)
	}

	if want4 := r.family == familyIPv4; addr.Is4() != want4 {
		return netip.Addr{}, fmt.Errorf("response %s is not an %s address", addr, r.family)
	}

	return addr, nil
}
