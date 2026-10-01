package cloudflare

import "github.com/dnspatch/dnspatch/httpx"

// Config holds the parameters of the cloudflare provider.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://api.cloudflare.com/client/v4" doc:"Base URL of the Cloudflare API"`
	ZoneID  string `toml:"zone_id,required" example:"023e105f4ecef8ad9ca31a8372d0c353" doc:"ID of the zone, shown on the zone's Overview page in the Cloudflare dashboard"`
	Zone    string `toml:"zone,required" example:"example.com" doc:"Domain name of the zone, for example example.com"`
	RRName  string `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	TTL     int    `toml:"ttl" default:"300" doc:"TTL in seconds for a record this provider creates; an existing record keeps its own TTL. 1 means automatic"`
	Proxied bool   `toml:"proxied" doc:"Whether a record this provider creates is proxied through Cloudflare (the orange cloud); an existing record keeps its own setting"`
	Token   string `toml:"token,required,secret" example:"${CF_TOKEN}" doc:"Cloudflare API token, scoped to Zone:DNS:Edit on this zone only"`

	httpx.ProxyConfig
}
