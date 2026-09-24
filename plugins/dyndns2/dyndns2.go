// Package dyndns2 provides a provider for services that speak the dyndns2
// update protocol, the one Dyn made popular: an authenticated GET request to
// an update URL with the host name and the address, answered with a short
// status word such as "good" or "nochg".
//
// The URL is a parameter, so one plugin serves NIC.RU, DNS-O-Matic, No-IP and
// every other service of the kind. The protocol has no way to set a TTL, so
// the record options are ignored.
//
// Wrappers for particular services live in subdirectories, such as nicru: each
// is a provider of its own that fills in the update URL and calls NewForService.
package dyndns2

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "dyndns2"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
