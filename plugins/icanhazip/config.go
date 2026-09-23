package icanhazip

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the icanhazip retriever.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://icanhazip.com" doc:"Base URL of the service; icanhazip.com is dual-stack and replies over whichever IP family the request arrives on"`
	Family  string `toml:"family" default:"ipv4" doc:"IP family to ask for: ipv4, ipv6, or dual to fetch both with two requests. Without a proxy each request is sent over its family; with a proxy the family of the connection is up to the proxy, and the reply is only checked to be of the right family"`

	httpx.DirectProxyConfig
}
