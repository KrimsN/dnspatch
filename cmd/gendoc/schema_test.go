package main

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dnspatch/dnspatch/plugin"
)

type schemaSample struct {
	Token   string        `toml:"token,required,secret" example:"${TOKEN}" doc:"API token"`
	Timeout time.Duration `toml:"timeout" default:"10s" doc:"Request timeout"`
	Retries int           `toml:"retries" default:"3"`
	Verify  bool          `toml:"verify" default:"true"`
	Addr    netip.Addr    `toml:"addr"`
	Tags    []string      `toml:"tags"`
	Auth    *struct {
		Key string `toml:"key,required,secret"`
	} `toml:"auth"`
}

func decodeSchema(t *testing.T, data []byte) schema {
	t.Helper()

	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	return s
}

func TestRenderSchemaDescribesFields(t *testing.T) {
	data, err := renderSchema(nil, map[string]reflect.Type{"sample": reflect.TypeFor[schemaSample]()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	s := decodeSchema(t, data)
	if s.SchemaVersion != schemaVersion || len(s.Plugins) != 1 {
		t.Fatalf("got version %d with %d plugins", s.SchemaVersion, len(s.Plugins))
	}

	p := s.Plugins[0]
	if p.Kind != plugin.KindProvider || p.Name != "sample" || !reflect.DeepEqual(p.BuildTags, []string{"sample", "providers_all"}) {
		t.Errorf("plugin header: %+v", p)
	}

	str := func(v string) *string { return &v }

	want := []schemaField{
		{Name: "token", Type: "string", Required: true, Example: str("${TOKEN}"), Doc: "API token", Secret: true},
		{Name: "timeout", Type: "duration", Default: str("10s"), Doc: "Request timeout"},
		{Name: "retries", Type: "integer", Default: str("3")},
		{Name: "verify", Type: "boolean", Default: str("true")},
		{Name: "addr", Type: "string"},
		{Name: "tags", Type: "array"},
		{Name: "auth.key", Type: "string", Required: true, RequiredIf: "auth", Secret: true},
	}

	if !reflect.DeepEqual(p.Fields, want) {
		t.Errorf("fields:\n got %+v\nwant %+v", p.Fields, want)
	}
}

func TestRenderSchemaWithoutPluginsHasEmptyList(t *testing.T) {
	data, err := renderSchema(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), `"plugins": []`) {
		t.Errorf("an empty schema should list no plugins as [], got %s", data)
	}
}

func TestRenderSchemaNamesTheBadPlugin(t *testing.T) {
	_, err := renderSchema(nil, nil, map[string]reflect.Type{"broken": reflect.TypeFor[string]()})
	if err == nil || !strings.Contains(err.Error(), `notifier "broken"`) {
		t.Errorf("error %v does not name the plugin", err)
	}
}

// The schema must list exactly the plugins that are declared in the source
// tree, and each of its build tags must really compile the plugin in, which
// is read from the constraint of the file in plugins/all that imports it.
func TestSchemaMatchesPluginsAndTags(t *testing.T) {
	if err := checkComplete(plugin.Default); err != nil {
		t.Skip(err)
	}

	data, err := renderSchema(plugin.Default.RetrieverConfigTypes(), plugin.Default.ProviderConfigTypes(), plugin.Default.NotifierConfigTypes())
	if err != nil {
		t.Fatal(err)
	}

	s := decodeSchema(t, data)

	listed := make(map[string]schemaPlugin, len(s.Plugins))
	for _, p := range s.Plugins {
		listed[string(p.Kind)+" "+p.Name] = p
	}

	for _, k := range plugin.Default.Known() {
		key := string(k.Kind) + " " + k.Name

		p, ok := listed[key]
		if !ok {
			t.Errorf("%s is declared but missing from the schema", key)
			continue
		}

		delete(listed, key)

		file, err := os.ReadFile(filepath.Join("..", "..", "plugins", "all", key[:strings.IndexByte(key, ' ')]+"_"+k.Name+".go"))
		if err != nil {
			t.Fatal(err)
		}

		constraint := ""
		for line := range strings.Lines(string(file)) {
			if after, ok := strings.CutPrefix(line, "//go:build "); ok {
				constraint = strings.TrimSpace(after)
			}
		}

		for _, tag := range p.BuildTags {
			if !strings.Contains(" "+constraint+" ", " "+tag+" ") {
				t.Errorf("%s: build tag %q is not in the constraint %q", key, tag, constraint)
			}
		}
	}

	for key := range listed {
		t.Errorf("%s is in the schema but not declared", key)
	}
}

func TestRunWritesSchemaOnRequest(t *testing.T) {
	if err := checkComplete(plugin.Default); err != nil {
		t.Skip(err)
	}

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "out", "schema.json")

	if err := run(filepath.Join(dir, "p.md"), filepath.Join(dir, "e.toml"), schemaPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}

	if s := decodeSchema(t, data); len(s.Plugins) == 0 {
		t.Error("the written schema lists no plugins")
	}
}
