// Package registry maps plugin type names to the factories that build them.
package registry

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/dnspatch/dnspatch/plugin/contract"
	"github.com/dnspatch/dnspatch/plugin/decode"
)

// providerFactory builds a Provider from raw configuration parameters.
type providerFactory func(params map[string]any) (contract.Provider, error)

// retrieverFactory builds a Retriever from raw configuration parameters.
type retrieverFactory func(params map[string]any) (contract.Retriever, error)

// notifierFactory builds a Notifier from raw configuration parameters.
type notifierFactory func(params map[string]any) (contract.Notifier, error)

// Kind is one of the kinds of plugin a Registry holds.
type Kind string

// The kinds of plugin. The values appear in error messages.
const (
	KindProvider  Kind = "provider"
	KindRetriever Kind = "retriever"
	KindNotifier  Kind = "notifier"
)

// Known is a plugin that exists in the source tree, whether or not this build
// has it compiled in. Hint says how to get it, and is shown in place of a bare
// "unknown type" when a configuration names it.
type Known struct {
	Kind Kind
	Name string
	Hint string
}

// entry[F] is one registered plugin: its factory and the type of its
// configuration struct, kept for documentation generation.
type entry[F any] struct {
	factory    F
	configType reflect.Type
}

// Registry maps plugin type names to factories. The zero Registry is empty and
// ready to use. A Registry is safe for concurrent use and must not be copied
// after first use.
type Registry struct {
	mu         sync.RWMutex
	providers  map[string]entry[providerFactory]
	retrievers map[string]entry[retrieverFactory]
	notifiers  map[string]entry[notifierFactory]
	known      []Known
}

// Default is the package-level registry that built-in plugins register into
// from their init functions.
var Default = NewRegistry()

// NewRegistry returns an empty registry, isolated from Default. Tests and
// programs that embed dnspatch as a library use it to control exactly which
// plugins are available.
func NewRegistry() *Registry {
	return &Registry{}
}

// RegisterProvider registers a provider type in Default.
//
// C is the plugin's configuration struct; parameters from the configuration
// file are decoded into it with Decode before build is called. It panics if
// name is empty, build is nil, C is not a struct, two fields of C take the same
// parameter name, a `toml` tag of C carries an unknown or repeated option or
// name is already registered, since all of these are programming errors that
// surface at process start.
func RegisterProvider[C any](name string, build func(cfg C) (contract.Provider, error)) {
	RegisterProviderIn(Default, name, build)
}

// RegisterRetriever registers a retriever type in Default. See RegisterProvider.
func RegisterRetriever[C any](name string, build func(cfg C) (contract.Retriever, error)) {
	RegisterRetrieverIn(Default, name, build)
}

// RegisterNotifier registers a notifier type in Default. See RegisterProvider.
// The configuration struct C must embed NotifierCommon, which supplies the
// topic_prefix parameter every notifier takes.
func RegisterNotifier[C contract.TopicNamer](name string, build func(cfg C) (contract.Notifier, error)) {
	RegisterNotifierIn(Default, name, build)
}

// RegisterProviderIn registers a provider type in the given registry.
// See RegisterProvider.
func RegisterProviderIn[C any](r *Registry, name string, build func(cfg C) (contract.Provider, error)) {
	checkRegistration[C]("provider", name, build == nil)

	factory := func(params map[string]any) (contract.Provider, error) {
		cfg, err := decode.Decode[C](params)
		if err != nil {
			return nil, err
		}
		return build(cfg)
	}

	register(&r.mu, &r.providers, "provider", name, entry[providerFactory]{factory: factory, configType: reflect.TypeFor[C]()})
}

// RegisterRetrieverIn registers a retriever type in the given registry.
// See RegisterProvider.
func RegisterRetrieverIn[C any](r *Registry, name string, build func(cfg C) (contract.Retriever, error)) {
	checkRegistration[C]("retriever", name, build == nil)

	factory := func(params map[string]any) (contract.Retriever, error) {
		cfg, err := decode.Decode[C](params)
		if err != nil {
			return nil, err
		}
		return build(cfg)
	}

	register(&r.mu, &r.retrievers, "retriever", name, entry[retrieverFactory]{factory: factory, configType: reflect.TypeFor[C]()})
}

// RegisterNotifierIn registers a notifier type in the given registry.
// See RegisterNotifier.
func RegisterNotifierIn[C contract.TopicNamer](r *Registry, name string, build func(cfg C) (contract.Notifier, error)) {
	checkRegistration[C]("notifier", name, build == nil)

	factory := func(params map[string]any) (contract.Notifier, error) {
		cfg, err := decode.Decode[C](params)
		if err != nil {
			return nil, err
		}

		n, err := build(cfg)
		if err != nil {
			return nil, err
		}

		return prefixed{Notifier: n, cfg: cfg}, nil
	}

	register(&r.mu, &r.notifiers, "notifier", name, entry[notifierFactory]{factory: factory, configType: reflect.TypeFor[C]()})
}

// checkRegistration rejects registration arguments that cannot work. C is the
// configuration type: Decode cannot fill anything but a struct, nor a struct
// whose fields share a parameter name or whose tags carry an option it does not
// know, and catching that here beats a failure on the first configuration file
// that names the plugin.
func checkRegistration[C any](kind, name string, nilBuild bool) {
	if name == "" {
		panic("plugin: " + kind + " name is empty")
	}
	if nilBuild {
		panic(fmt.Sprintf("plugin: %s %q has a nil constructor", kind, name))
	}
	if t := reflect.TypeFor[C](); t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("plugin: %s %q: configuration type %s is not a struct", kind, name, t))
	}

	// configFields panics on duplicated parameter names.
	decode.Fields(reflect.TypeFor[C]())
}

// register adds an entry to one of the registry's maps, creating the map on
// first use so that the zero Registry works.
func register[F any](mu *sync.RWMutex, entries *map[string]entry[F], kind, name string, e entry[F]) {
	mu.Lock()
	defer mu.Unlock()

	if *entries == nil {
		*entries = make(map[string]entry[F])
	}

	if _, ok := (*entries)[name]; ok {
		panic(fmt.Sprintf("plugin: %s %q is already registered", kind, name))
	}
	(*entries)[name] = e
}

// BuildProvider builds the provider registered under name, decoding params
// into its configuration struct.
func (r *Registry) BuildProvider(name string, params map[string]any) (contract.Provider, error) {
	r.mu.RLock()
	found, ok := r.providers[name]
	r.mu.RUnlock()

	if !ok {
		return nil, r.unknownType(KindProvider, name)
	}

	prv, err := found.factory(params)
	if err != nil {
		return nil, fmt.Errorf("provider %q: %w", name, err)
	}

	return prv, nil
}

// BuildRetriever builds the retriever registered under name, decoding params
// into its configuration struct.
func (r *Registry) BuildRetriever(name string, params map[string]any) (contract.Retriever, error) {
	r.mu.RLock()
	found, ok := r.retrievers[name]
	r.mu.RUnlock()

	if !ok {
		return nil, r.unknownType(KindRetriever, name)
	}

	ret, err := found.factory(params)
	if err != nil {
		return nil, fmt.Errorf("retriever %q: %w", name, err)
	}

	return ret, nil
}

// BuildNotifier builds the notifier registered under name, decoding params
// into its configuration struct. What it publishes goes under the topic the
// configuration's topic_prefix gives.
func (r *Registry) BuildNotifier(name string, params map[string]any) (contract.Notifier, error) {
	r.mu.RLock()
	found, ok := r.notifiers[name]
	r.mu.RUnlock()

	if !ok {
		return nil, r.unknownType(KindNotifier, name)
	}

	n, err := found.factory(params)
	if err != nil {
		return nil, fmt.Errorf("notifier %q: %w", name, err)
	}

	return n, nil
}

// Declare records that a plugin exists in the source tree, so that a
// configuration naming one this build lacks gets hint instead of a bare
// "unknown type". Declaring a plugin does not register it.
func (r *Registry) Declare(kind Kind, name, hint string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.known = append(r.known, Known{Kind: kind, Name: name, Hint: hint})
}

// Known lists every declared plugin, in the order they were declared.
func (r *Registry) Known() []Known {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Clone(r.known)
}

// Registered reports whether a plugin of the kind is registered under name.
func (r *Registry) Registered(kind Kind, name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch kind {
	case KindProvider:
		_, ok := r.providers[name]
		return ok
	case KindRetriever:
		_, ok := r.retrievers[name]
		return ok
	case KindNotifier:
		_, ok := r.notifiers[name]
		return ok
	default:
		return false
	}
}

// ProviderConfigTypes returns the configuration struct type of every
// registered provider, keyed by type name. Documentation generation reads the
// struct tags from these types.
func (r *Registry) ProviderConfigTypes() map[string]reflect.Type {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return configTypesOf(r.providers)
}

// RetrieverConfigTypes returns the configuration struct type of every
// registered retriever, keyed by type name.
func (r *Registry) RetrieverConfigTypes() map[string]reflect.Type {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return configTypesOf(r.retrievers)
}

// NotifierConfigTypes returns the configuration struct type of every
// registered notifier, keyed by type name.
func (r *Registry) NotifierConfigTypes() map[string]reflect.Type {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return configTypesOf(r.notifiers)
}

// configTypesOf extracts the configuration types of a registry map.
func configTypesOf[F any](entries map[string]entry[F]) map[string]reflect.Type {
	types := make(map[string]reflect.Type, len(entries))
	for name, e := range entries {
		types[name] = e.configType
	}

	return types
}

// keysOf returns the registered names of a registry map, sorted.
func keysOf[F any](entries map[string]entry[F]) []string {
	return slices.Sorted(maps.Keys(entries))
}

// unknownType explains an unregistered type name. A plugin that is declared
// but not compiled into this build says how to get it; otherwise the names that
// are registered are listed, so that a typo is easy to spot.
func (r *Registry) unknownType(kind Kind, name string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var registered []string
	switch kind {
	case KindProvider:
		registered = keysOf(r.providers)
	case KindRetriever:
		registered = keysOf(r.retrievers)
	case KindNotifier:
		registered = keysOf(r.notifiers)
	}

	for _, k := range r.known {
		if k.Kind == kind && k.Name == name {
			return fmt.Errorf("%s type %q is not compiled into this build: %s", kind, name, k.Hint)
		}
	}

	if len(registered) == 0 {
		return fmt.Errorf("unknown %s type %q (no %s types are registered)", kind, name, kind)
	}

	return fmt.Errorf("unknown %s type %q (registered: %s)", kind, name, strings.Join(registered, ", "))
}

// prefixed is a Notifier that publishes every event under the topic its
// configuration derives from the name it is given.
type prefixed struct {
	contract.Notifier
	cfg contract.TopicNamer
}

func (p prefixed) Publish(ctx context.Context, instance string, payload []byte) error {
	return p.Notifier.Publish(ctx, p.cfg.Topic(instance), payload)
}
