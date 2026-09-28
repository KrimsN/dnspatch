// Package cloudflare provides a provider for zones hosted on Cloudflare,
// managed through its REST API
// (https://developers.cloudflare.com/api/resources/dns/subresources/records/).
//
// Authentication is a static API token, scoped to Zone:DNS:Edit on a single
// zone; unlike Yandex Cloud there is no key exchange, so the token is sent
// as-is on every call.
package cloudflare

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "cloudflare"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
