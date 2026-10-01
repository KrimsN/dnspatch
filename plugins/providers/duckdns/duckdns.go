// Package duckdns provides a provider for domains registered at DuckDNS
// (https://www.duckdns.org/spec.jsp), a free dynamic DNS service.
//
// DuckDNS is not a dyndns2 service: the token travels in the query string
// rather than as HTTP Basic auth, and the answer is a bare OK or KO instead
// of dyndns2's good/nochg vocabulary, so it needs its own plugin.
package duckdns

import "github.com/dnspatch/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "duckdns"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
