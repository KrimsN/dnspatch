// Package plugin defines the public contract of dnspatch: the Retriever,
// Provider and Notifier interfaces, the plugin registry and parameter decoding.
//
// The package is public so that third-party modules can implement the
// interfaces and register their own plugins. It only re-exports: the
// interfaces live in plugin/contract, the registry in plugin/registry and the
// decoder in plugin/decode, and a plugin needs no import but this one.
//
// A plugin is a configuration struct plus a constructor that turns it into a
// Retriever, a Provider or a Notifier. Registering it makes its type name usable in the
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
	"github.com/dnspatch/dnspatch/plugin/contract"
	"github.com/dnspatch/dnspatch/plugin/decode"
	"github.com/dnspatch/dnspatch/plugin/registry"
)

// The interfaces and value types of a plugin. See package contract.
type (
	// Retriever reports the current public IP address(es) of the machine.
	Retriever = contract.Retriever
	// Addresses carries the address of each family to write.
	Addresses = contract.Addresses
	// RecordOptions carries options that apply to a write, independent of the address.
	RecordOptions = contract.RecordOptions
	// Provider writes an IP address to a DNS record.
	Provider = contract.Provider
	// Notifier delivers one already-serialized event to a message broker.
	Notifier = contract.Notifier
	// NotifierCommon holds the parameters every notifier takes; the
	// configuration struct of a notifier must embed it.
	NotifierCommon = contract.NotifierCommon
	// TopicNamer is what the configuration struct of a notifier must be;
	// embedding NotifierCommon satisfies it.
	TopicNamer = contract.TopicNamer
)

// The registry and what it reports. See package registry.
type (
	// Registry maps plugin type names to factories.
	Registry = registry.Registry
	// Kind is one of the kinds of plugin a Registry holds.
	Kind = registry.Kind
	// Known is a plugin that exists in the source tree, whether or not this build compiled it in.
	Known = registry.Known
)

// The kinds of plugin. The values appear in error messages.
const (
	KindProvider  = registry.KindProvider
	KindRetriever = registry.KindRetriever
	KindNotifier  = registry.KindNotifier
)

// Default is the package-level registry that built-in plugins register into
// from their init functions.
var Default = registry.Default

// NewRegistry returns an empty registry, isolated from Default. Tests and
// programs that embed dnspatch as a library use it to control exactly which
// plugins are available.
func NewRegistry() *Registry {
	return registry.NewRegistry()
}

// RegisterProvider registers a provider type in Default. See
// registry.RegisterProvider for what it panics on.
func RegisterProvider[C any](name string, build func(cfg C) (Provider, error)) {
	registry.RegisterProvider(name, build)
}

// RegisterRetriever registers a retriever type in Default. See RegisterProvider.
func RegisterRetriever[C any](name string, build func(cfg C) (Retriever, error)) {
	registry.RegisterRetriever(name, build)
}

// RegisterNotifier registers a notifier type in Default. See RegisterProvider.
// The configuration struct C must embed NotifierCommon, which supplies the
// topic_prefix parameter every notifier takes.
func RegisterNotifier[C TopicNamer](name string, build func(cfg C) (Notifier, error)) {
	registry.RegisterNotifier(name, build)
}

// RegisterProviderIn registers a provider type in the given registry.
// See RegisterProvider.
func RegisterProviderIn[C any](r *Registry, name string, build func(cfg C) (Provider, error)) {
	registry.RegisterProviderIn(r, name, build)
}

// RegisterRetrieverIn registers a retriever type in the given registry.
// See RegisterProvider.
func RegisterRetrieverIn[C any](r *Registry, name string, build func(cfg C) (Retriever, error)) {
	registry.RegisterRetrieverIn(r, name, build)
}

// RegisterNotifierIn registers a notifier type in the given registry.
// See RegisterNotifier.
func RegisterNotifierIn[C TopicNamer](r *Registry, name string, build func(cfg C) (Notifier, error)) {
	registry.RegisterNotifierIn(r, name, build)
}

// Decode converts a plugin's raw parameters into its configuration struct,
// applying the `toml`, `default` and `required` struct tags. See package
// decode for the rules.
func Decode[C any](params map[string]any) (C, error) {
	return decode.Decode[C](params)
}
