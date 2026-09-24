// Package nicru provides a provider for domains at NIC.RU (RU-CENTER),
// updated through its Dynamic DNS service.
//
// It is the dyndns2 provider with the NIC.RU update URL filled in, so that a
// configuration says type = "nicru" and need not know the address. Use dyndns2
// itself for another service, or to point this one at a different URL.
package nicru

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "nicru"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, endpoint)
	})
}
