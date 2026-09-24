package dynu

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the dynu provider.
type Config struct {
	Username string `toml:"username" required:"true" doc:"Dynu account login"`
	Password string `toml:"password" required:"true" example:"${PASSWORD}" doc:"Password of the account, or the separate IP update password that Dynu lets you set in the account, which is the safer choice"`
	Hostname string `toml:"hostname" required:"true" example:"home.example.com" doc:"Full domain name of the record to update, for example home.example.com"`

	httpx.ProxyConfig
}
