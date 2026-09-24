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
//		APIToken string        `toml:"api_token,required,secret"`
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
	"time"
)

// Retriever reports the current public IP address(es) of the machine.
//
// The returned Addresses must have at least one valid field; a retriever that
// only ever discovers one family (the common case) leaves the other at its
// zero value. Implementations must respect ctx and abort any network call
// when it is cancelled.
type Retriever interface {
	GetAddresses(ctx context.Context) (Addresses, error)
}

// Addresses carries the address of each family to write. An invalid field
// (the zero netip.Addr) means that family is not touched: an existing record
// of that type is left as it is. At least one field must be valid.
type Addresses struct {
	V4, V6 netip.Addr
}

// RecordOptions carries options that apply to a write, independent of the
// address itself. The zero value means "use the provider's own default for
// every option"; a provider that cannot honour an option ignores it.
type RecordOptions struct {
	// TTL overrides the provider's configured TTL when positive. A provider
	// whose service does not support setting a TTL ignores it.
	TTL time.Duration
}

// Provider writes an IP address to a DNS record.
//
// The record type is derived from the address family: V4 means an A record,
// V6 means AAAA. Implementations must respect ctx and abort any network call
// when it is cancelled.
type Provider interface {
	Update(ctx context.Context, addrs Addresses, opts RecordOptions) error
}
