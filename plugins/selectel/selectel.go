// Package selectel provides a provider for zones hosted on Selectel DNS
// Hosting, managed through the public DNS API v2
// (https://docs.selectel.ru/en/api/dns-actual).
//
// Unlike most providers, the API has no static, unlimited-lifetime key: every
// call carries a project-scoped IAM token obtained from Selectel's identity
// service (Keystone), valid for 24 hours. The provider fetches and caches
// that token itself, re-authenticating when it is close to expiry or a call
// comes back 401.
package selectel

import "github.com/KrimsN/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "selectel"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
