package dyn

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the dyn provider.
type Config struct {
	Username string `toml:"username" required:"true" doc:"Login of the Dyn account"`
	Password string `toml:"password" required:"true" example:"${PASSWORD}" doc:"Updater client key of the account, created in the account settings; it is not the account password"`
	Hostname string `toml:"hostname" required:"true" example:"home.example.com" doc:"Full domain name of the host to update, for example home.example.com"`

	httpx.ProxyConfig
}
