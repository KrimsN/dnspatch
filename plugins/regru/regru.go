// Package regru provides a provider for zones hosted on the REG.RU DNS
// servers, managed through REG.API 2 (https://api.reg.ru/api/regru2).
//
// The API can only manage zones served by the REG.RU name servers. The TTL of
// a record cannot be set through it: REG.RU keeps a single TTL per zone.
package regru

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "regru"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
