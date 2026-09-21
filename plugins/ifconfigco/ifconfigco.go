// Package ifconfigco provides a retriever backed by the ifconfig.co service.
//
// The service is dual-stack, so the address it reports depends on which IP
// family the request arrives over. The "family" parameter pins the family
// instead of leaving it to the operating system.
//
// ifconfig.co asks automated clients to send at most one request per minute;
// the default polling interval of dnspatch stays well below that rate.
package ifconfigco

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the retriever in the configuration file.
const Name = "ifconfigco"

func init() {
	plugin.RegisterRetriever(Name, func(cfg Config) (plugin.Retriever, error) {
		return newRetriever(cfg, nil)
	})
}
