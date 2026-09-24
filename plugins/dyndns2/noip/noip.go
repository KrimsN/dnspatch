// Package noip provides a provider for hosts at No-IP, updated through
// the No-IP Dynamic DNS service.
//
// It is the dyndns2 provider with the No-IP update URL filled in, so that a
// configuration says type = "noip" and need not know the address. Use dyndns2
// itself for another service, or to point this one at a different URL.
package noip

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "noip"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, endpoint)
	})
}
