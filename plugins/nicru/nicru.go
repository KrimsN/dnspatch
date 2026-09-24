// Package nicru provides a provider for domains at NIC.RU (RU-CENTER),
// updated through its Dynamic DNS service.
//
// It is the dyndns2 provider with the NIC.RU update URL filled in, so that a
// configuration says type = "nicru" and need not know the address. Use dyndns2
// itself for another service, or to point this one at a different URL.
package nicru

import (
	"github.com/KrimsN/dnspatch/internal/httpx"
	"github.com/KrimsN/dnspatch/plugin"
	"github.com/KrimsN/dnspatch/plugins/dyndns2"
)

// Name is the type name of the provider in the configuration file.
const Name = "nicru"

// endpoint is the update URL of the NIC.RU Dynamic DNS service.
const endpoint = "https://api.nic.ru/dyndns/update"

// Config holds the parameters of the nicru provider.
type Config struct {
	Username string `toml:"username" required:"true" doc:"Login of the NIC.RU account or contract that may update the domain; the Dynamic DNS service must be switched on for it"`
	Password string `toml:"password" required:"true" example:"${PASSWORD}" doc:"Password for that login"`
	Hostname string `toml:"hostname" required:"true" example:"home.example.com" doc:"Full domain name of the record to update. NIC.RU changes the A records with this name in every zone of the contract, not only in the zone the domain belongs to"`

	httpx.ProxyConfig
}

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return build(cfg, endpoint)
	})
}

// build hands the configuration to dyndns2 with the update URL of the service.
// Tests pass the address of a fake service instead of the real one.
func build(cfg Config, baseURL string) (plugin.Provider, error) {
	return dyndns2.New(dyndns2.Config{
		BaseURL:     baseURL,
		Username:    cfg.Username,
		Password:    cfg.Password,
		Hostname:    cfg.Hostname,
		ProxyConfig: cfg.ProxyConfig,
	})
}
