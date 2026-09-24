package noip

import (
	"github.com/KrimsN/dnspatch/plugin"
	"github.com/KrimsN/dnspatch/plugins/dyndns2"
)

// endpoint is the update URL of the No-IP Dynamic DNS service.
const endpoint = "https://dynupdate.no-ip.com/nic/update"

// newProvider hands the configuration to dyndns2 with the update URL of the service.
// Tests pass the address of a fake service instead of the real one.
func newProvider(cfg Config, baseURL string) (plugin.Provider, error) {
	return dyndns2.NewForService(dyndns2.Config{
		BaseURL:     baseURL,
		IPParam:     "myip",
		IPv6Param:   "myipv6",
		Username:    cfg.Username,
		Password:    cfg.Password,
		Hostname:    cfg.Hostname,
		ProxyConfig: cfg.ProxyConfig,
	})
}
