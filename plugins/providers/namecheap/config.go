package namecheap

import "github.com/dnspatch/dnspatch/internal/httpx"

// Config holds the parameters of the namecheap provider.
type Config struct {
	BaseURL  string `toml:"base_url" default:"https://dynamicdns.park-your-domain.com/update" doc:"Update URL of the Namecheap Dynamic DNS API"`
	Host     string `toml:"host,required" example:"@" doc:"Host to update: @ for the bare domain, or the host record's name"`
	Domain   string `toml:"domain,required" example:"example.com" doc:"Domain registered at Namecheap, exactly as it appears in the account (the API is case-sensitive)"`
	Password string `toml:"password,required,secret" example:"${NAMECHEAP_DDNS_PASSWORD}" doc:"Dynamic DNS Password from the domain's Advanced DNS tab, not the account password"`

	httpx.ProxyConfig
}
