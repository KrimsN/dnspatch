package plugin_test

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

type fakeConfig struct {
	Token string `toml:"token" required:"true"`
}

type fakeProvider struct {
	cfg fakeConfig
}

func (p *fakeProvider) Update(context.Context, plugin.Addresses, plugin.RecordOptions) error {
	return nil
}

type fakeRetriever struct {
	cfg fakeConfig
}

func (r *fakeRetriever) GetIPAddress(context.Context) (netip.Addr, error) {
	return netip.MustParseAddr("192.0.2.1"), nil
}

func newProvider(cfg fakeConfig) (plugin.Provider, error) { return &fakeProvider{cfg: cfg}, nil }

func newRetriever(cfg fakeConfig) (plugin.Retriever, error) { return &fakeRetriever{cfg: cfg}, nil }

func TestBuildProvider(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "fake", newProvider)

	built, err := registry.BuildProvider("fake", map[string]any{"token": "secret"})
	if err != nil {
		t.Fatalf("BuildProvider: %v", err)
	}

	prv, ok := built.(*fakeProvider)
	if !ok {
		t.Fatalf("BuildProvider returned %T, want *fakeProvider", built)
	}
	if prv.cfg.Token != "secret" {
		t.Errorf("Token = %q, want %q", prv.cfg.Token, "secret")
	}
}

func TestBuildRetriever(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", newRetriever)

	built, err := registry.BuildRetriever("fake", map[string]any{"token": "secret"})
	if err != nil {
		t.Fatalf("BuildRetriever: %v", err)
	}

	ret, ok := built.(*fakeRetriever)
	if !ok {
		t.Fatalf("BuildRetriever returned %T, want *fakeRetriever", built)
	}
	if ret.cfg.Token != "secret" {
		t.Errorf("Token = %q, want %q", ret.cfg.Token, "secret")
	}
}

func TestBuildUnknownTypeListsRegistered(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "alpha", newProvider)
	plugin.RegisterProviderIn(registry, "beta", newProvider)

	_, err := registry.BuildProvider("alpha-typo", nil)
	if err == nil {
		t.Fatal("BuildProvider succeeded, want an unknown type error")
	}

	want := `unknown provider type "alpha-typo" (registered: alpha, beta)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestBuildUnknownTypeOnEmptyRegistry(t *testing.T) {
	_, err := plugin.NewRegistry().BuildRetriever("anything", nil)
	if err == nil {
		t.Fatal("BuildRetriever succeeded, want an unknown type error")
	}

	if !strings.Contains(err.Error(), "no retriever types are registered") {
		t.Errorf("error = %q, want it to say nothing is registered", err)
	}
}

func TestBuildProviderWrapsDecodeError(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "fake", newProvider)

	_, err := registry.BuildProvider("fake", map[string]any{})
	if err == nil {
		t.Fatal("BuildProvider succeeded, want a decode error")
	}

	message := err.Error()
	if !strings.Contains(message, `provider "fake"`) || !strings.Contains(message, `parameter "token" is required`) {
		t.Errorf("error = %q, want the provider name and the parameter name", err)
	}
}

func TestBuildProviderPropagatesConstructorError(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "broken", func(fakeConfig) (plugin.Provider, error) {
		return nil, fmt.Errorf("cannot reach the API")
	})

	_, err := registry.BuildProvider("broken", map[string]any{"token": "secret"})
	if err == nil {
		t.Fatal("BuildProvider succeeded, want the constructor error")
	}

	if !strings.Contains(err.Error(), "cannot reach the API") {
		t.Errorf("error = %q, want the constructor error", err)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "fake", newProvider)

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("the second registration did not panic")
		}
		if !strings.Contains(fmt.Sprint(recovered), `provider "fake" is already registered`) {
			t.Errorf("panic = %v, want it to name the duplicate", recovered)
		}
	}()

	plugin.RegisterProviderIn(registry, "fake", newProvider)
}

func TestRegistrationRejectsEmptyName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering an empty name did not panic")
		}
	}()

	plugin.RegisterRetrieverIn(plugin.NewRegistry(), "", newRetriever)
}

func TestRegistrationRejectsNilConstructor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a nil constructor did not panic")
		}
	}()

	plugin.RegisterProviderIn(plugin.NewRegistry(), "fake", (func(fakeConfig) (plugin.Provider, error))(nil))
}

// defaultRuns numbers the runs of TestPackageRegistrationWritesToDefault: Default
// cannot be emptied, so under -count or -shuffle each run registers a name of
// its own instead of colliding with the previous one.
var defaultRuns atomic.Int64

func TestPackageRegistrationWritesToDefault(t *testing.T) {
	name := fmt.Sprintf("package-level-test-provider-%d", defaultRuns.Add(1))

	plugin.RegisterProvider(name, newProvider)

	if _, err := plugin.Default.BuildProvider(name, map[string]any{"token": "secret"}); err != nil {
		t.Fatalf("Default.BuildProvider: %v", err)
	}

	if _, err := plugin.NewRegistry().BuildProvider(name, nil); err == nil {
		t.Error("a fresh registry knows the plugin, want registration to stay in Default")
	}
}

func TestPackageRetrieverRegistrationWritesToDefault(t *testing.T) {
	name := fmt.Sprintf("package-level-test-retriever-%d", defaultRuns.Add(1))

	plugin.RegisterRetriever(name, newRetriever)

	if _, err := plugin.Default.BuildRetriever(name, map[string]any{"token": "secret"}); err != nil {
		t.Fatalf("Default.BuildRetriever: %v", err)
	}

	if _, err := plugin.NewRegistry().BuildRetriever(name, nil); err == nil {
		t.Error("a fresh registry knows the plugin, want registration to stay in Default")
	}
}

func TestConfigTypes(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "fake", newProvider)
	plugin.RegisterRetrieverIn(registry, "fake", newRetriever)

	want := reflect.TypeFor[fakeConfig]()

	if got := registry.ProviderConfigTypes()["fake"]; got != want {
		t.Errorf("provider config type = %v, want %v", got, want)
	}
	if got := registry.RetrieverConfigTypes()["fake"]; got != want {
		t.Errorf("retriever config type = %v, want %v", got, want)
	}
}

func TestConcurrentRegistrationAndBuild(t *testing.T) {
	registry := plugin.NewRegistry()

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(2)

		go func() {
			defer wg.Done()
			plugin.RegisterProviderIn(registry, fmt.Sprintf("provider-%d", i), newProvider)
		}()

		go func() {
			defer wg.Done()
			plugin.RegisterRetrieverIn(registry, fmt.Sprintf("retriever-%d", i), newRetriever)
			registry.ProviderConfigTypes()
			_, _ = registry.BuildProvider("provider-0", map[string]any{"token": "secret"})
		}()
	}
	wg.Wait()

	if got := len(registry.ProviderConfigTypes()); got != 16 {
		t.Errorf("registered providers = %d, want 16", got)
	}
	if got := len(registry.RetrieverConfigTypes()); got != 16 {
		t.Errorf("registered retrievers = %d, want 16", got)
	}
}

func TestRegistrationRejectsNonStructConfig(t *testing.T) {
	tests := map[string]func(){
		"string": func() {
			plugin.RegisterProviderIn(plugin.NewRegistry(), "fake", func(string) (plugin.Provider, error) { return nil, nil })
		},
		"pointer to struct": func() {
			plugin.RegisterRetrieverIn(plugin.NewRegistry(), "fake", func(*fakeConfig) (plugin.Retriever, error) { return nil, nil })
		},
	}

	for name, register := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("registering a non-struct configuration did not panic")
				}
				if !strings.Contains(fmt.Sprint(recovered), "is not a struct") {
					t.Errorf("panic = %v, want it to say the type is not a struct", recovered)
				}
			}()

			register()
		})
	}
}

func TestRegistrationRejectsDuplicatedParameterName(t *testing.T) {
	tests := map[string]func(){
		"provider": func() {
			plugin.RegisterProviderIn(plugin.NewRegistry(), "fake", func(duplicated) (plugin.Provider, error) { return nil, nil })
		},
		"retriever": func() {
			plugin.RegisterRetrieverIn(plugin.NewRegistry(), "fake", func(duplicated) (plugin.Retriever, error) { return nil, nil })
		},
	}

	for name, register := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("registering a configuration with a duplicated parameter did not panic")
				}
				if !strings.Contains(fmt.Sprint(recovered), `First and Second`) {
					t.Errorf("panic = %v, want it to name both fields", recovered)
				}
			}()

			register()
		})
	}
}

func TestZeroRegistryIsUsable(t *testing.T) {
	var registry plugin.Registry

	if _, err := registry.BuildProvider("fake", nil); err == nil || !strings.Contains(err.Error(), "no provider types are registered") {
		t.Errorf("BuildProvider on an empty registry: error = %v, want the unknown-type error", err)
	}

	plugin.RegisterProviderIn(&registry, "fake", newProvider)
	plugin.RegisterRetrieverIn(&registry, "fake", newRetriever)

	if _, err := registry.BuildProvider("fake", map[string]any{"token": "secret"}); err != nil {
		t.Errorf("BuildProvider: %v", err)
	}
	if _, err := registry.BuildRetriever("fake", map[string]any{"token": "secret"}); err != nil {
		t.Errorf("BuildRetriever: %v", err)
	}
}
