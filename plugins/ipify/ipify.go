// Package ipify provides a retriever backed by the ipify.org service.
//
// api64.ipify.org is dual-stack: the address it reports depends on which IP
// family the request arrives over. The "family" parameter pins the family
// instead of leaving it to the operating system; "dual" asks for both by
// making two requests, one per family, since the service has no single
// response carrying both addresses.
//
// By default the retriever connects directly and ignores the HTTP_PROXY and
// HTTPS_PROXY environment variables: through a proxy the service reports the
// address of the proxy, not of this host. The "proxy" parameter sends the
// request through a proxy anyway, for the case where the address of the proxy
// is the one wanted. The family is then no longer pinned on the connection,
// since the proxy chooses it; the reply is still checked against it.
package ipify

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the retriever in the configuration file.
const Name = "ipify"

func init() {
	plugin.RegisterRetriever(Name, func(cfg Config) (plugin.Retriever, error) {
		return newRetriever(cfg, nil)
	})
}
