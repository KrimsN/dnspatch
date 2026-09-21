package ifconfigco

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the ifconfigco retriever.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://ifconfig.co" doc:"Base URL of the service; change it to use a self-hosted instance"`
	Family  string `toml:"family" default:"ipv4" doc:"IP family to ask for: ipv4 or ipv6. Without a proxy the request is sent over that family; with a proxy the family of the connection is up to the proxy, and the reply is only checked to be of this family"`

	httpx.DirectProxyConfig
}
