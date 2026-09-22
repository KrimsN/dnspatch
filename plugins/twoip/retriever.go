package twoip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/KrimsN/dnspatch/internal/httpx"
)

// maxBody caps how much of a response is read: the JSON body is a few
// hundred bytes, anything longer is not what we asked for.
const maxBody = 1 << 12

const requestTimeout = 30 * time.Second

type retriever struct {
	endpoint string
	client   *http.Client
}

// newRetriever validates cfg and builds the retriever. A nil client builds
// one from cfg.Proxy; tests pass their own.
func newRetriever(cfg Config, client *http.Client) (*retriever, error) {
	if err := httpx.ValidateBaseURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("base_url: %w", err)
	}

	if client == nil {
		var err error
		if client, err = newClient(cfg.Proxy); err != nil {
			return nil, err
		}
	}

	return &retriever{
		endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/",
		client:   client,
	}, nil
}

// newClient builds the client the retriever uses. An empty proxy is treated
// like "direct": unlike a provider, a retriever must not follow the proxy
// environment variables unless explicitly told to, or it risks reporting the
// address of the proxy instead of this host's.
func newClient(proxy string) (*http.Client, error) {
	if strings.TrimSpace(proxy) == "" || httpx.IsDirect(proxy) {
		transport := httpx.NewTransport()
		transport.Proxy = nil

		return &http.Client{Timeout: requestTimeout, Transport: transport}, nil
	}

	return httpx.NewClient(proxy, requestTimeout)
}

// lookupResponse is the JSON body the anonymous lookup endpoint replies with.
// A request the service refuses, for example one asking for a paid method
// without a token, carries an "error" field and no "ip" instead.
type lookupResponse struct {
	IP    string `json:"ip"`
	Error string `json:"error"`
}

// GetIPAddress asks the service for the public IPv4 address of this host.
func (r *retriever) GetIPAddress(ctx context.Context) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("Accept", "application/json")

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

	return parse(body)
}

// parse turns a response body into an IPv4 address.
func parse(body []byte) (netip.Addr, error) {
	var payload lookupResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return netip.Addr{}, fmt.Errorf("response is not valid JSON: %s", httpx.Snippet(body))
	}
	if payload.Error != "" {
		return netip.Addr{}, fmt.Errorf("service error: %s", payload.Error)
	}

	addr, err := netip.ParseAddr(payload.IP)
	if err != nil {
		return netip.Addr{}, fmt.Errorf(`response "ip" field is not an IP address: %s`, httpx.Snippet(body))
	}
	addr = addr.Unmap()

	if addr.Zone() != "" || !addr.IsGlobalUnicast() {
		return netip.Addr{}, fmt.Errorf("response %s is not a global unicast address", addr)
	}
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("response %s is not an IPv4 address", addr)
	}

	return addr, nil
}
