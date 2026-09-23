// Package beget provides a provider for zones hosted on Beget, managed
// through its JSON-over-HTTP API (https://beget.com/en/kb/api/dns-administration-functions).
//
// The API has no notion of a single record set: dns/changeRecords replaces
// every record of every type at a name in one call, so a caller that sends
// only the type it cares about wipes out everything else already there (a
// known failure mode of other DDNS clients against this API). This provider
// always reads the full record set with dns/getData first and writes it back
// unchanged apart from the one type (A or AAAA) it owns.
//
// The API also has no per-record TTL parameter, so this provider has no TTL
// configuration to offer and ignores plugin.RecordOptions.TTL.
package beget

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "beget"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
