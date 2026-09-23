package ipify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/KrimsN/dnspatch/internal/httpx"
	"github.com/KrimsN/dnspatch/plugin"
)

const (
	familyIPv4 = "ipv4"
	familyIPv6 = "ipv6"
	// familyBoth asks for both families in one retriever; familyIPv64 is an
	// accepted alias, normalized to familyBoth at construction.
	familyBoth  = "both"
	familyIPv64 = "ipv64"

	// maxBody caps how much of a response is read: an address is a few dozen
	// bytes, anything longer is not what we asked for.
	maxBody = 1 << 10

	requestTimeout = 30 * time.Second
)

type retriever struct {
	endpoint string
	family   string

	// client is used when family is ipv4 or ipv6. v4Client and v6Client are
	// used when family is "both": one request per family, since the service
	// has no single response carrying both addresses.
	client             *http.Client
	v4Client, v6Client *http.Client
}

// newRetriever validates cfg and builds the retriever. A nil client selects
// one that dials over the configured IP family (or one per family, for
// "both"); tests pass their own, reused for every family it needs.
func newRetriever(cfg Config, client *http.Client) (*retriever, error) {
	family := strings.ToLower(cfg.Family)
	if family == familyIPv64 {
		family = familyBoth
	}
	if family != familyIPv4 && family != familyIPv6 && family != familyBoth {
		return nil, fmt.Errorf(`family: must be "ipv4", "ipv6" or "both" (alias "ipv64"), got %q`, cfg.Family)
	}

	if err := httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	r := &retriever{
		endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/",
		family:   family,
	}

	if family != familyBoth {
		r.client = client
		if r.client == nil {
			var err error
			if r.client, err = newClient(cfg.Proxy, family); err != nil {
				return nil, err
			}
		}
		return r, nil
	}

	r.v4Client, r.v6Client = client, client
	if client == nil {
		var err error
		if r.v4Client, err = newClient(cfg.Proxy, familyIPv4); err != nil {
			return nil, err
		}
		if r.v6Client, err = newClient(cfg.Proxy, familyIPv6); err != nil {
			return nil, err
		}
	}

	return r, nil
}

// newClient builds the client the retriever uses. Without a proxy, that is
// direct or empty, the connection is pinned to the IP family. With one, the
// connection to the proxy is left alone, since the family that matters is the
// one the proxy connects to the service over, and the reply check in
// parseAddr is what enforces the family.
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

// GetAddresses asks the service for the public address(es) of the configured
// family. For "both" it makes two requests, one per family over the
// respective pinned client, and fails if either one does: a partial result
// is not what "both" was configured for. Configure two single-family
// retrievers instead for fallback across independent sources.
func (r *retriever) GetAddresses(ctx context.Context) (plugin.Addresses, error) {
	if r.family != familyBoth {
		addr, err := r.fetch(ctx, r.client, r.family)
		if err != nil {
			return plugin.Addresses{}, err
		}
		if r.family == familyIPv4 {
			return plugin.Addresses{V4: addr}, nil
		}
		return plugin.Addresses{V6: addr}, nil
	}

	v4, errV4 := r.fetch(ctx, r.v4Client, familyIPv4)
	v6, errV6 := r.fetch(ctx, r.v6Client, familyIPv6)
	if errV4 != nil || errV6 != nil {
		return plugin.Addresses{}, errors.Join(errV4, errV6)
	}
	return plugin.Addresses{V4: v4, V6: v6}, nil
}

// fetch asks the service, over client, for the public address of family and
// checks the reply matches it.
func (r *retriever) fetch(ctx context.Context, client *http.Client, family string) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := client.Do(req)
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

	return parseAddr(body, family)
}

// parseAddr turns a response body into an address of the given family.
func parseAddr(body []byte, family string) (netip.Addr, error) {
	text := strings.TrimSpace(string(body))

	addr, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("response is not an IP address: %s", httpx.Snippet(body))
	}
	addr = addr.Unmap()

	if addr.Zone() != "" || !addr.IsGlobalUnicast() {
		return netip.Addr{}, fmt.Errorf("response %s is not a global unicast address", addr)
	}

	if want4 := family == familyIPv4; addr.Is4() != want4 {
		return netip.Addr{}, fmt.Errorf("response %s is not an %s address", addr, family)
	}

	return addr, nil
}
