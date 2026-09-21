// Package ifconfigco provides a retriever backed by the ifconfig.co service.
//
// The service is dual-stack, so the address it reports depends on which IP
// family the request arrives over. The "family" parameter pins the family
// instead of leaving it to the operating system.
//
// By default the retriever connects directly and ignores the HTTP_PROXY and
// HTTPS_PROXY environment variables: through a proxy the service reports the
// address of the proxy, not of this host. The "proxy" parameter sends the
// request through a proxy anyway, for the case where the address of the proxy
// is the one wanted. The family is then no longer pinned on the connection,
// since the proxy chooses it; the reply is still checked against it.
//
// ifconfig.co asks automated clients to send at most one request per minute.
// The default polling interval of dnspatch, five minutes, is within that
// limit; an instance that polls more often gets a warning in the log.
package ifconfigco

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the retriever in the configuration file.
const Name = "ifconfigco"

func init() {
	plugin.RegisterRetriever(Name, func(cfg Config) (plugin.Retriever, error) {
		return newRetriever(cfg, nil)
	})
}
