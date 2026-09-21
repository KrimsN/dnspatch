package plugin

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// providerFactory builds a Provider from raw configuration parameters.
type providerFactory func(params map[string]any) (Provider, error)

// retrieverFactory builds a Retriever from raw configuration parameters.
type retrieverFactory func(params map[string]any) (Retriever, error)

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
// parameter name or name is already registered, since all of these are
// programming errors that surface at process start.
func RegisterProvider[C any](name string, build func(cfg C) (Provider, error)) {
	RegisterProviderIn(Default, name, build)
}

// RegisterRetriever registers a retriever type in Default. See RegisterProvider.
func RegisterRetriever[C any](name string, build func(cfg C) (Retriever, error)) {
	RegisterRetrieverIn(Default, name, build)
}

// RegisterProviderIn registers a provider type in the given registry.
// See RegisterProvider.
func RegisterProviderIn[C any](r *Registry, name string, build func(cfg C) (Provider, error)) {
	checkRegistration[C]("provider", name, build == nil)

	factory := func(params map[string]any) (Provider, error) {
		cfg, err := Decode[C](params)
		if err != nil {
			return nil, err
		}
		return build(cfg)
	}

	register(&r.mu, &r.providers, "provider", name, entry[providerFactory]{factory: factory, configType: reflect.TypeFor[C]()})
}

// RegisterRetrieverIn registers a retriever type in the given registry.
// See RegisterProvider.
func RegisterRetrieverIn[C any](r *Registry, name string, build func(cfg C) (Retriever, error)) {
	checkRegistration[C]("retriever", name, build == nil)

	factory := func(params map[string]any) (Retriever, error) {
		cfg, err := Decode[C](params)
		if err != nil {
			return nil, err
		}
		return build(cfg)
	}

	register(&r.mu, &r.retrievers, "retriever", name, entry[retrieverFactory]{factory: factory, configType: reflect.TypeFor[C]()})
}

// checkRegistration rejects registration arguments that cannot work. C is the
// configuration type: Decode cannot fill anything but a struct, nor a struct
// whose fields share a parameter name, and catching that here beats a failure
// on the first configuration file that names the plugin.
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
	configFields(reflect.TypeFor[C]())
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
func (r *Registry) BuildProvider(name string, params map[string]any) (Provider, error) {
	r.mu.RLock()
	found, ok := r.providers[name]
	var names []string
	if !ok {
		names = keysOf(r.providers)
	}
	r.mu.RUnlock()

	if !ok {
		return nil, unknownTypeError("provider", name, names)
	}

	prv, err := found.factory(params)
	if err != nil {
		return nil, fmt.Errorf("provider %q: %w", name, err)
	}

	return prv, nil
}

// BuildRetriever builds the retriever registered under name, decoding params
// into its configuration struct.
func (r *Registry) BuildRetriever(name string, params map[string]any) (Retriever, error) {
	r.mu.RLock()
	found, ok := r.retrievers[name]
	var names []string
	if !ok {
		names = keysOf(r.retrievers)
	}
	r.mu.RUnlock()

	if !ok {
		return nil, unknownTypeError("retriever", name, names)
	}

	ret, err := found.factory(params)
	if err != nil {
		return nil, fmt.Errorf("retriever %q: %w", name, err)
	}

	return ret, nil
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

// unknownTypeError explains an unregistered type name and lists the names
// that are registered.
func unknownTypeError(kind, name string, registered []string) error {
	if len(registered) == 0 {
		return fmt.Errorf("unknown %s type %q (no %s types are registered)", kind, name, kind)
	}

	return fmt.Errorf("unknown %s type %q (registered: %s)", kind, name, strings.Join(registered, ", "))
}
