package timeweb

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the timeweb provider.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://api.timeweb.cloud/api/v1" doc:"Base URL of the DNS API"`
	Zone    string `toml:"zone,required" example:"example.com" doc:"Domain name of the zone, for example example.com"`
	RRName  string `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	TTL     int    `toml:"ttl" default:"300" doc:"TTL in seconds for a record this provider creates; an existing record keeps its own TTL"`
	Token   string `toml:"token,required,secret" example:"${TW_TOKEN}" doc:"Timeweb Cloud API JWT token, from the control panel under API keys"`

	httpx.ProxyConfig
}
