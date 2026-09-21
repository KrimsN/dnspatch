package main

import (
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/plugin"
)

type exampleSample struct {
	Token   string        `toml:"token" required:"true" doc:"API token. Keep it secret."`
	Zone    string        `toml:"zone" required:"true" example:"example.com" doc:"Zone"`
	Timeout time.Duration `toml:"timeout" default:"10s" doc:"Request timeout"`
	Retries int           `toml:"retries" default:"3"`
	Verify  bool          `toml:"verify" default:"true"`
	Ratio   float64       `toml:"ratio" default:"2"`
	Tags    []string      `toml:"tags" example:"[\"a\", \"b\"]"`
	Addr    netip.Addr    `toml:"addr"`
	Empty   string        `toml:"empty" default:""`
	Auth    struct {
		User string `toml:"user" required:"true" example:"admin" doc:"Login"`
	} `toml:"auth"`
	Extra *struct {
		Key string `toml:"key" required:"true" doc:"Key of the optional block"`
	} `toml:"extra"`
}

func TestExampleShowsEveryKindOfParameter(t *testing.T) {
	got, err := renderExample(nil, map[string]reflect.Type{"sample": reflect.TypeFor[exampleSample]()})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"[provider.sample]\ntype = \"sample\"\n",
		"# API token.\ntoken = \"CHANGE_ME\"\n",
		"zone = \"example.com\"\n",
		"\n# timeout = \"10s\"\n",
		"\n# retries = 3\n",
		"\n# verify = true\n",
		"\n# ratio = 2.0\n",
		"\n# tags = [\"a\", \"b\"]\n",
		"\n# addr = \"\"\n",
		"\n# empty = \"\"\n",
		"\n# Login\nauth.user = \"admin\"\n",
		"\n# Key of the optional block Required once extra is set.\n# extra.key = \"CHANGE_ME\"\n",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}

	if strings.Contains(string(got), "Keep it secret") {
		t.Error("the description is not cut after its first sentence")
	}
}

func TestExampleWithoutPluginsHasNoInstance(t *testing.T) {
	got, err := renderExample(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(got), "[[instance]]") {
		t.Errorf("an instance is written although there is nothing to refer to:\n%s", got)
	}
}

func TestExampleIsRepeatable(t *testing.T) {
	plugins := make(map[string]reflect.Type)
	for _, name := range []string{"e", "b", "d", "a", "c"} {
		plugins[name] = reflect.TypeFor[exampleSample]()
	}

	first, err := renderExample(plugins, plugins)
	if err != nil {
		t.Fatal(err)
	}

	for range 50 {
		again, err := renderExample(plugins, plugins)
		if err != nil {
			t.Fatal(err)
		}

		if string(again) != string(first) {
			t.Fatal("two renders of the same plugins differ")
		}
	}

	if !strings.Contains(string(first), "[instance.retriever]\nref = \"a\"") {
		t.Error("the instance does not use the first retriever by name")
	}
}

func TestExampleRejectsParametersItCannotShow(t *testing.T) {
	type noExample struct {
		Retries int `toml:"retries" required:"true"`
	}

	type badDefault struct {
		Retries int `toml:"retries" default:"many"`
	}

	type badExample struct {
		Verify bool `toml:"verify" example:"maybe"`
	}

	type badDuration struct {
		Timeout time.Duration `toml:"timeout" default:"soon"`
	}

	type noTOMLForm struct {
		Hook func() `toml:"hook"`
	}

	type badKey struct {
		Odd string `toml:"has space"`
	}

	tests := map[string]reflect.Type{
		"required without example": reflect.TypeFor[noExample](),
		"bad integer default":      reflect.TypeFor[badDefault](),
		"bad boolean example":      reflect.TypeFor[badExample](),
		"bad duration default":     reflect.TypeFor[badDuration](),
		"no TOML form":             reflect.TypeFor[noTOMLForm](),
		"key that needs quoting":   reflect.TypeFor[badKey](),
	}

	for name, typ := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := renderExample(nil, map[string]reflect.Type{"broken": typ})
			if err == nil || !strings.Contains(err.Error(), `provider "broken"`) {
				t.Errorf("error %v does not reject and name the plugin", err)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	tests := map[string]string{
		"One sentence":                              "One sentence",
		"First one. Second one.":                    "First one.",
		"For example socks5://h:1. Then more.":      "For example socks5://h:1.",
		"REG.RU login used for API calls":           "REG.RU login used for API calls",
		"Version 1.2 is required. Older is ignored": "Version 1.2 is required.",
		"Ends with a lower-case start. and more":    "Ends with a lower-case start. and more",
		"  Spaces\n  and lines.  ":                  "Spaces and lines.",
	}

	for doc, want := range tests {
		if got := summary(doc); got != want {
			t.Errorf("summary(%q) = %q, want %q", doc, got, want)
		}
	}
}

func TestShortDuration(t *testing.T) {
	tests := map[time.Duration]string{
		5 * time.Minute:              "5m",
		time.Hour:                    "1h",
		90 * time.Minute:             "1h30m",
		10 * time.Second:             "10s",
		time.Minute + 30*time.Second: "1m30s",
	}

	for d, want := range tests {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTOMLString(t *testing.T) {
	tests := map[string]string{
		"plain":       `"plain"`,
		`say "hi"`:    `"say \"hi\""`,
		`back\slash`:  `"back\\slash"`,
		"two\nlines":  `"two\nlines"`,
		"tab\there":   `"tab\there"`,
		"${ENV_NAME}": `"${ENV_NAME}"`,
	}

	for text, want := range tests {
		if got := tomlString(text); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", text, got, want)
		}
	}
}

// A user copies the file, edits the values and runs it. Whatever the plugins
// need to start has to be in it, and it has to load and build as written.
func TestExampleLoadsAndBuildsAsWritten(t *testing.T) {
	doc, err := renderExample(plugin.Default.RetrieverConfigTypes(), plugin.Default.ProviderConfigTypes())
	if err != nil {
		t.Fatal(err)
	}

	for _, ref := range regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`).FindAllStringSubmatch(string(doc), -1) {
		t.Setenv(ref[1], "value")
	}

	cfg, err := config.Parse(doc)
	if err != nil {
		t.Fatalf("the example does not load: %v\n%s", err, doc)
	}

	if len(cfg.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(cfg.Instances))
	}

	in := cfg.Instances[0]

	if _, err := plugin.Default.BuildRetriever(in.Retriever.Type, in.Retriever.Params); err != nil {
		t.Errorf("the retriever of the example does not build: %v", err)
	}

	for _, p := range in.Providers {
		if _, err := plugin.Default.BuildProvider(p.Type, p.Params); err != nil {
			t.Errorf("the provider %q of the example does not build: %v", p.Type, err)
		}
	}
}

// Every definition in the file must be usable, not only the two the instance
// refers to, so each is built once through an instance of its own.
func TestExampleDefinitionsAllBuild(t *testing.T) {
	retrievers, providers := plugin.Default.RetrieverConfigTypes(), plugin.Default.ProviderConfigTypes()

	doc, err := renderExample(retrievers, providers)
	if err != nil {
		t.Fatal(err)
	}

	for _, ref := range regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`).FindAllStringSubmatch(string(doc), -1) {
		t.Setenv(ref[1], "value")
	}

	for retriever := range retrievers {
		for provider := range providers {
			text := string(doc[:strings.Index(string(doc), "[[instance]]")]) +
				"[[instance]]\nname = \"t\"\n[instance.retriever]\nref = \"" + retriever + "\"\n" +
				"[[instance.provider]]\nref = \"" + provider + "\"\n"

			cfg, err := config.Parse([]byte(text))
			if err != nil {
				t.Fatalf("%s + %s: %v", retriever, provider, err)
			}

			in := cfg.Instances[0]

			if _, err := plugin.Default.BuildRetriever(in.Retriever.Type, in.Retriever.Params); err != nil {
				t.Errorf("retriever %q: %v", retriever, err)
			}

			if _, err := plugin.Default.BuildProvider(in.Providers[0].Type, in.Providers[0].Params); err != nil {
				t.Errorf("provider %q: %v", provider, err)
			}
		}
	}
}

func TestTOMLStringEscapesControlCharacters(t *testing.T) {
	got := tomlString("bell\x07")

	if strings.ContainsRune(got, 7) || !strings.Contains(got, "u0007") {
		t.Errorf("tomlString of a control character = %q, want an escape", got)
	}
}
