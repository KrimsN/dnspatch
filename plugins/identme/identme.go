// Package identme provides a retriever backed by the ident.me service.
//
// ident.me is dual-stack: the address it reports depends on which IP family
// the request arrives over. The service also documents 4.ident.me and
// 6.ident.me hosts, whose DNS records only carry an A or an AAAA record
// respectively, but the "family" parameter pins the family on the connection
// instead, the same way the ipify and icanhazip retrievers do for their
// dual-stack host: one code path, and a family check on the reply either way.
//
// By default the retriever connects directly and ignores the HTTP_PROXY and
// HTTPS_PROXY environment variables: through a proxy the service reports the
// address of the proxy, not of this host. The "proxy" parameter sends the
// request through a proxy anyway, for the case where the address of the
// proxy is the one wanted. The family is then no longer pinned on the
// connection, since the proxy chooses it; the reply is still checked
// against it.
package identme

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the retriever in the configuration file.
const Name = "identme"

func init() {
	plugin.RegisterRetriever(Name, func(cfg Config) (plugin.Retriever, error) {
		return newRetriever(cfg, nil)
	})
}
