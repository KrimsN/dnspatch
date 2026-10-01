// Package timeweb provides a provider for zones hosted on Timeweb Cloud,
// managed through its REST API (https://timeweb.cloud/api-docs#tag/Domeny).
//
// Authentication is a static JWT token issued from the control panel under
// API keys; unlike Selectel there is no token exchange or expiry to track,
// so the token is sent as-is on every call.
package timeweb

import "github.com/dnspatch/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "timeweb"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
