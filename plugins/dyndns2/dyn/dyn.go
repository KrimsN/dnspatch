// Package dyn provides a provider for hosts at Dyn (dyn.com, once DynDNS), the
// service the dyndns2 protocol comes from.
//
// It is the dyndns2 provider with the Dyn update URL filled in, so that a
// configuration says type = "dyn" and need not know the address. Use dyndns2
// itself for another service, or to point this one at a different URL.
package dyn

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "dyn"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, endpoint)
	})
}
