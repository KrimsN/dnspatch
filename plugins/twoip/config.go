package twoip

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the 2ip.io retriever.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://api.2ip.io" doc:"Base URL of the service; change it to use a self-hosted instance"`

	httpx.DirectProxyConfig
}
