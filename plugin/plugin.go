// Package plugin defines the public contract of dnspatch: the Retriever and
// Provider interfaces, the plugin registry and parameter decoding.
//
// The package is public so that third-party modules can implement the
// interfaces and register their own plugins.
package plugin

// TODO: Retriever reports the current public IP address.
// TODO: Provider writes an address to a DNS record.
