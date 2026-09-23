// Package netif provides a retriever that reads the address from a local
// network interface instead of asking an external service.
//
// For IPv6 this is usually the better source: the address is already on the
// interface, no request leaves the host, and the answer does not depend on
// which route a service happens to see. For IPv4 it is only useful when the
// interface holds the public address itself, that is, not behind NAT.
//
// Which address is chosen when an interface has several is decided by
// selectAddr; the rule is documented on the "name" parameter and in the
// README.
package netif

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the retriever in the configuration file.
const Name = "interface"

func init() {
	plugin.RegisterRetriever(Name, func(cfg Config) (plugin.Retriever, error) {
		return newRetriever(cfg, nil)
	})
}
