package noip

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the noip provider.
type Config struct {
	Hostname string `toml:"hostname,required" example:"home.example.com" doc:"Full domain name of the host to update, for example home.example.com or a name under ddns.net"`
	Username string `toml:"username,required" doc:"Login for the update: a DDNS key of the host, or the No-IP account itself"`
	Password string `toml:"password,required,secret" example:"${PASSWORD}" doc:"Password for that login"`

	httpx.ProxyConfig
}
