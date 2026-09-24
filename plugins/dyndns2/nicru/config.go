package nicru

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the nicru provider.
type Config struct {
	Hostname string `toml:"hostname,required" example:"home.example.com" doc:"Full domain name of the record to update. NIC.RU changes the A records with this name in every zone of the contract, not only in the zone the domain belongs to"`
	Username string `toml:"username,required" doc:"Login of the NIC.RU account or contract that may update the domain; the Dynamic DNS service must be switched on for it"`
	Password string `toml:"password,required,secret" example:"${PASSWORD}" doc:"Password for that login"`

	httpx.ProxyConfig
}
