package main

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/internal/paramspec"
	"github.com/KrimsN/dnspatch/plugin"
)

// fakeDNSConfig has one parameter of each kind the summary tells apart: a
// required one, optional ones and a secret.
type fakeDNSConfig struct {
	Zone     string `toml:"zone,required"`
	RRName   string `toml:"rr_name"`
	TTL      int    `toml:"ttl"`
	Password string `toml:"password,secret"`
}

// summaryOf runs --check-config on a config whose only instance has the given
// provider tables and entries, and returns what it printed. The registry has
// two provider types, "dns" and "other", with the same parameters.
func summaryOf(t *testing.T, providers string) string {
	t.Helper()

	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{}, nil
	})
	for _, name := range []string{"dns", "other"} {
		plugin.RegisterProviderIn(registry, name, func(fakeDNSConfig) (plugin.Provider, error) {
			return fakeProviderStub{}, nil
		})
	}

	path := writeConfig(t, `
[retriever.r]
type = "fake"
`+providers)

	var stdout, stderr bytes.Buffer

	if code := run(context.Background(), []string{"--config", path, "--check-config"}, &stdout, &stderr, registry); code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitOK, stderr.String())
	}

	return stdout.String()
}

func TestCheckConfigTellsProvidersOfOneTypeApart(t *testing.T) {
	tests := map[string]struct {
		providers string
		want      string
	}{
		"the only provider of its type is shown by name": {
			providers: `
[provider.home]
type = "dns"
zone = "example.com"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "home"
`,
			want: "providers=[home]",
		},
		"one definition with a different zone and record": {
			providers: `
[provider.dns]
type    = "dns"
zone    = "example.com"
rr_name = "home"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref     = "dns"
zone    = "example.org"
rr_name = "office"
`,
			want: "providers=[dns(zone=example.com, rr_name=home), dns(zone=example.org, rr_name=office)]",
		},
		"inline providers": {
			providers: `
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
type = "dns"
zone = "a.example"
[[instance.provider]]
type = "dns"
zone = "b.example"
`,
			want: "providers=[dns(zone=a.example), dns(zone=b.example)]",
		},
		"different definitions of one type": {
			providers: `
[provider.home]
type = "dns"
zone = "example.com"
[provider.office]
type = "dns"
zone = "example.org"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "home"
[[instance.provider]]
ref = "office"
`,
			want: "providers=[home(zone=example.com), office(zone=example.org)]",
		},
		"only the parameters that differ are listed": {
			providers: `
[provider.dns]
type    = "dns"
zone    = "example.com"
rr_name = "home"
ttl     = 60
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref = "dns"
ttl = 300
`,
			want: "providers=[dns(ttl=60), dns(ttl=300)]",
		},
		"a parameter set in one provider only": {
			providers: `
[provider.dns]
type = "dns"
zone = "example.com"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref     = "dns"
rr_name = "office"
[[instance.provider]]
ref = "dns"
ttl = 300
`,
			want: "providers=[dns(rr_name=office), dns(ttl=300)]",
		},
		"providers of different types are not compared": {
			providers: `
[provider.first]
type = "dns"
zone = "example.com"
[provider.second]
type = "other"
zone = "example.org"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "first"
[[instance.provider]]
ref = "second"
`,
			want: "providers=[first, second]",
		},
		"providers of one type that do not differ": {
			providers: `
[provider.home]
type = "dns"
zone = "example.com"
[provider.office]
type = "dns"
zone = "example.com"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "home"
[[instance.provider]]
ref = "office"
`,
			want: "providers=[home, office]",
		},
		"parameter names match ignoring case": {
			providers: `
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
type    = "dns"
zone    = "a.example"
rr_name = "home"
[[instance.provider]]
type    = "dns"
zone    = "b.example"
RR_NAME = "home"
`,
			want: "providers=[dns(zone=a.example), dns(zone=b.example)]",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := summaryOf(t, tt.providers); !strings.Contains(got, tt.want) {
				t.Errorf("stdout = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// TestCheckConfigShowsSecretsOnlyWhenNothingElseDiffers covers the secret
// parameters: their values never reach the output, a mask stands in for one
// when secrets are all that tell providers apart, and nothing at all when
// something else does.
func TestCheckConfigShowsSecretsOnlyWhenNothingElseDiffers(t *testing.T) {
	tests := map[string]struct {
		providers string
		want      string
	}{
		"only the secret differs": {
			providers: `
[provider.dns]
type     = "dns"
zone     = "example.com"
password = "hunter2"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref      = "dns"
password = "swordfish"
`,
			want: "providers=[dns(password=***), dns(password=***)]",
		},
		"a secret is left out next to a difference that is not one": {
			providers: `
[provider.dns]
type     = "dns"
zone     = "example.com"
password = "hunter2"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref      = "dns"
zone     = "example.org"
password = "swordfish"
`,
			want: "providers=[dns(zone=example.com), dns(zone=example.org)]",
		},
		"a secret is left out even when the other difference is in another provider": {
			providers: `
[provider.dns]
type     = "dns"
zone     = "example.com"
password = "hunter2"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref      = "dns"
ttl      = 300
password = "swordfish"
`,
			want: "providers=[dns, dns(ttl=300)]",
		},
		"a secret that is the same everywhere is not listed": {
			providers: `
[provider.dns]
type     = "dns"
zone     = "example.com"
password = "hunter2"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref  = "dns"
zone = "example.org"
`,
			want: "providers=[dns(zone=example.com), dns(zone=example.org)]",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := summaryOf(t, tt.providers)

			if !strings.Contains(got, tt.want) {
				t.Errorf("stdout = %q, want it to contain %q", got, tt.want)
			}

			for _, secret := range []string{"hunter2", "swordfish"} {
				if strings.Contains(got, secret) {
					t.Errorf("stdout = %q, must not contain the secret %q", got, secret)
				}
			}
		})
	}
}

func TestDisplayValue(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		secret bool
		want   string
	}{
		{"string", "example.com", false, "example.com"},
		{"integer", int64(60), false, "60"},
		{"boolean", true, false, "true"},
		{"secret string", "hunter2", true, maskedValue},
		{"secret integer", int64(1234), true, maskedValue},
		{"table", map[string]any{"password": "hunter2"}, false, elidedValue},
		{"list", []any{"a", "b"}, false, elidedValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayValue(tt.value, tt.secret); got != tt.want {
				t.Errorf("displayValue(%v, secret=%v) = %q, want %q", tt.value, tt.secret, got, tt.want)
			}
		})
	}
}

// TestParametersExampledAsAReferenceAreSecret guards the secret option: a
// parameter whose example is a ${NAME} reference holds a secret, and the
// summary prints the values of parameters that are not marked as one. A plugin
// that forgets the option fails here rather than leaking into a CI log.
func TestParametersExampledAsAReferenceAreSecret(t *testing.T) {
	kinds := map[string]map[string]reflect.Type{
		"provider":  plugin.Default.ProviderConfigTypes(),
		"retriever": plugin.Default.RetrieverConfigTypes(),
	}

	for kind, types := range kinds {
		if len(types) == 0 {
			t.Fatalf("no %s types are registered, the check would pass without looking at anything", kind)
		}

		for name, configType := range types {
			fields, err := paramspec.Fields(configType)
			if err != nil {
				t.Fatalf("%s %q: %v", kind, name, err)
			}

			for _, f := range fields {
				if strings.HasPrefix(f.Example, "${") && !f.Secret {
					t.Errorf("%s %q: parameter %q is exampled as the reference %q, so it holds a secret, but its toml tag lacks the secret option",
						kind, name, f.Key, f.Example)
				}
			}
		}
	}
}
