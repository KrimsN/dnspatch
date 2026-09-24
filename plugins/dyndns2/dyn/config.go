package dyn

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the dyn provider.
type Config struct {
	Hostname string `toml:"hostname,required" example:"home.example.com" doc:"Full domain name of the host to update, for example home.example.com"`
	Username string `toml:"username,required" doc:"Login of the Dyn account"`
	Password string `toml:"password,required,secret" example:"${PASSWORD}" doc:"Updater client key of the account, created in the account settings; it is not the account password"`

	httpx.ProxyConfig
}
