// Package plugin defines the public contract of dnspatch: the Retriever and
// Provider interfaces, the plugin registry and parameter decoding.
//
// The package is public so that third-party modules can implement the
// interfaces and register their own plugins.
//
// A plugin is a configuration struct plus a constructor that turns it into a
// Retriever or a Provider. Registering it makes its type name usable in the
// configuration file:
//
//	type Config struct {
//		APIToken string        `toml:"api_token" required:"true"`
//		Timeout  time.Duration `toml:"timeout" default:"10s"`
//	}
//
//	func init() {
//		plugin.RegisterProvider("example", func(cfg Config) (plugin.Provider, error) {
//			return &exampleProvider{cfg: cfg}, nil
//		})
//	}
package plugin

import (
	"context"
	"net/netip"
)

// Retriever reports the current public IP address of the machine.
//
// The returned address must be valid; returning an invalid netip.Addr is an
// error on the retriever's side. Implementations must respect ctx and abort
// any network call when it is cancelled.
type Retriever interface {
	GetIPAddress(ctx context.Context) (netip.Addr, error)
}

// Provider writes an IP address to a DNS record.
//
// The record type is derived from the address: addr.Is4() means an A record,
// anything else means AAAA. Implementations must respect ctx and abort any
// network call when it is cancelled.
type Provider interface {
	SetIPAddress(ctx context.Context, addr netip.Addr) error
}
