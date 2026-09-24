// Package dynu provides a provider for hosts at Dynu, updated through
// the Dynu Dynamic DNS service.
//
// It is the dyndns2 provider with the Dynu update URL filled in, so that a
// configuration says type = "dynu" and need not know the address. Use dyndns2
// itself for another service, or to point this one at a different URL.
package dynu

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "dynu"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, endpoint)
	})
}
