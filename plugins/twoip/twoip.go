// Package twoip provides a retriever backed by the api.2ip.io service.
//
// The anonymous lookup endpoint that api.2ip.io exposes has no IPv6 address
// of its own, so the service can only ever be reached, and can only ever
// report, an IPv4 address: there is no family parameter to pin one or the
// other, unlike a dual-stack service.
//
// By default the retriever connects directly and ignores the HTTP_PROXY and
// HTTPS_PROXY environment variables: through a proxy the service reports the
// address of the proxy, not of this host. The "proxy" parameter sends the
// request through a proxy anyway, for the case where the address of the
// proxy is the one wanted.
package twoip

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the retriever in the configuration file.
const Name = "2ip"

func init() {
	plugin.RegisterRetriever(Name, func(cfg Config) (plugin.Retriever, error) {
		return newRetriever(cfg, nil)
	})
}
