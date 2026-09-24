package beget

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the beget provider.
type Config struct {
	BaseURL  string `toml:"base_url" default:"https://api.beget.com/api" doc:"Base URL of the API"`
	Zone     string `toml:"zone,required" example:"example.com" doc:"Domain name of the zone, for example example.com"`
	RRName   string `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	Username string `toml:"username,required" doc:"Beget account login used for API calls"`
	Password string `toml:"password,required,secret" example:"${PASSWORD}" doc:"Beget account password"`

	httpx.ProxyConfig
}
