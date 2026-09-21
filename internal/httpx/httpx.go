// Package httpx builds the HTTP clients that plugins use to reach their
// services, including the optional proxy in front of them.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Direct is the proxy value that turns proxying off altogether: the client
// connects straight to the service and ignores HTTP_PROXY, HTTPS_PROXY and
// NO_PROXY.
const Direct = "direct"

// ProxyConfig is the configuration block a provider embeds to make its API
// requests go through a proxy. Left empty, the client follows the proxy
// environment variables.
type ProxyConfig struct {
	Proxy string `toml:"proxy" example:"socks5://user:pass@203.0.113.5:1080" doc:"URL of a proxy to send API requests through, for example socks5://user:pass@203.0.113.5:1080. Schemes: socks5 and socks5h (the proxy resolves the API host name), http and https. Percent-encode special characters in the login and password. Use it when the API only accepts requests from a fixed address. The word direct connects without a proxy and ignores the proxy environment variables. Empty: connect directly, or through HTTP_PROXY/HTTPS_PROXY from the environment. The address retriever has its own proxy parameter and does not use this one"`
}

// DirectProxyConfig is the configuration block a retriever embeds. Unlike a
// provider, a retriever connects without a proxy unless told otherwise, and
// never follows the proxy environment variables: through a proxy it learns the
// address of the proxy, not its own.
type DirectProxyConfig struct {
	Proxy string `toml:"proxy" default:"direct" doc:"URL of a proxy to send requests through: socks5, socks5h, http or https, optionally with user:pass@ (percent-encode special characters). The default, direct, connects without a proxy and ignores the proxy environment variables. Behind a proxy the service reports the address the proxy connects from, not the address of this host, so set it only when that is the address you want"`
}

// IsDirect reports whether value asks for a connection without any proxy.
func IsDirect(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), Direct)
}

// ParseProxy validates a proxy URL. The URL may hold a login and password, so
// no error quotes it or any part of it.
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// The parser's own message repeats the URL, or a piece of the
		// password, so it is dropped.
		return nil, errors.New("not a valid URL; percent-encode special characters in the login and password")
	}

	switch u.Scheme {
	case "socks5", "socks5h", "http", "https":
	default:
		return nil, errors.New(`scheme must be one of socks5, socks5h, http or https, or the word "direct" for no proxy`)
	}

	if u.Hostname() == "" {
		return nil, errors.New("host is missing")
	}

	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("only scheme, login, password, host and port are allowed")
	}

	return u, nil
}

// NewClient returns a client for requests to a service. With an empty
// proxyURL it connects the way net/http does by default, which honours
// HTTP_PROXY, HTTPS_PROXY and NO_PROXY from the environment. With Direct it
// connects without a proxy and ignores the environment. With a proxy URL every
// request goes through that proxy and the environment is ignored.
//
// Both socks5 and socks5h hand the destination host name to the proxy, so the
// proxy resolves it.
func NewClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	switch {
	case IsDirect(proxyURL):
		transport.Proxy = nil
	case proxyURL != "":
		u, err := ParseProxy(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}

	return &http.Client{Timeout: timeout, Transport: transport}, nil
}
