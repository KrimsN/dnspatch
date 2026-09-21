// Package selectelv1 provides a provider for the legacy Selectel DNS hosting
// API (https://api.selectel.ru/domains/v1), authenticated with a static token.
//
// Selectel has marked this API as outdated in favour of DNS hosting v2, which
// only accepts IAM tokens that expire after 24 hours. Both are meant to
// coexist as separate plugins.
package selectelv1

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "selectel_v1"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
