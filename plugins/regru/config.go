package regru

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the regru provider.
type Config struct {
	Username string `toml:"username,required" doc:"REG.RU account login used for API calls"`
	Password string `toml:"password,required,secret" example:"${PASSWORD}" doc:"API password; set an alternative password for the API in the REG.RU account and allow the address dnspatch runs from"`
	Zone     string `toml:"zone,required" example:"example.com" doc:"Domain name of the zone, for example example.com"`
	RRName   string `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	BaseURL  string `toml:"base_url" default:"https://api.reg.ru/api/regru2" doc:"Base URL of the API"`

	httpx.ProxyConfig
}
