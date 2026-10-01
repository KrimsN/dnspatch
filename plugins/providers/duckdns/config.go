package duckdns

import "github.com/dnspatch/dnspatch/httpx"

// Config holds the parameters of the duckdns provider.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://www.duckdns.org/update" doc:"Update URL of the DuckDNS API"`
	Domain  string `toml:"domain,required" example:"myhost" doc:"Subdomain registered at DuckDNS, without the .duckdns.org suffix"`
	Token   string `toml:"token,required,secret" example:"${DUCKDNS_TOKEN}" doc:"DuckDNS account token, shown on the DuckDNS dashboard"`

	httpx.ProxyConfig
}
