package nicru

import (
	"github.com/KrimsN/dnspatch/plugin"
	"github.com/KrimsN/dnspatch/plugins/dyndns2"
)

// endpoint is the update URL of the NIC.RU Dynamic DNS service.
const endpoint = "https://api.nic.ru/dyndns/update"

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
