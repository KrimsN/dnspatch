package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// header defines one retriever and one provider for tests that only care
// about the instance part.
const header = `
[retriever.home]
type = "ifconfigco"

[provider.main]
type      = "selectel"
api_token = "secret"
zone      = "a.com"
rr_name   = "@"
`

func setFullEnv(t *testing.T) {
	t.Helper()
	t.Setenv("UNIFI_TOKEN", "unifi-secret")
	t.Setenv("SELECTEL_TOKEN", "selectel-secret")
}

func loadFull(t *testing.T) Config {
	t.Helper()
	setFullEnv(t)

	cfg, err := Load(filepath.Join("testdata", "full.toml"))
	if err != nil {
		t.Fatalf("Load(full.toml): %v", err)
	}

	return cfg
}

func mustParse(t *testing.T, doc string) Config {
	t.Helper()

	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	return cfg
}

func parseError(t *testing.T, doc string) string {
	t.Helper()

	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatal("Parse succeeded, want an error")
	}

	return err.Error()
}

func wantContains(t *testing.T, got string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("error %q does not contain %q", got, want)
		}
	}
}

func TestFullExampleParses(t *testing.T) {
	cfg := loadFull(t)

	if cfg.Interval != DefaultInterval {
		t.Errorf("global interval = %v, want %v", cfg.Interval, DefaultInterval)
	}

	if len(cfg.Instances) != 2 {
		t.Fatalf("got %d instances, want 2", len(cfg.Instances))
	}

	homelab, office := cfg.Instances[0], cfg.Instances[1]
	if homelab.Name != "homelab" || office.Name != "office" {
		t.Errorf("instance order = %q, %q; want homelab, office", homelab.Name, office.Name)
	}

	if homelab.Retriever.Type != "ifconfigco" || len(homelab.Retriever.Params) != 0 {
		t.Errorf("homelab retriever = %+v", homelab.Retriever)
	}

	if len(homelab.Providers) != 3 {
		t.Fatalf("homelab has %d providers, want 3", len(homelab.Providers))
	}

	wantSelectel := map[string]any{
		"api_token": "selectel-secret",
		"zone":      "homelab.com",
		"rr_name":   "@",
		"ttl":       int64(60),
	}
	if got := homelab.Providers[0].Params; !reflect.DeepEqual(got, wantSelectel) {
		t.Errorf("homelab provider 1 params = %v, want %v", got, wantSelectel)
	}
}

func TestSpecialRecordNames(t *testing.T) {
	cfg := loadFull(t)
	homelab := cfg.Instances[0]

	if got := homelab.Providers[0].Params["rr_name"]; got != "@" {
		t.Errorf(`rr_name = %v, want "@"`, got)
	}

	if got := homelab.Providers[2].Params["rr_name"]; got != "*" {
		t.Errorf(`rr_name = %v, want "*"`, got)
	}

	if got := homelab.Providers[2].Params["api_key"]; got != "literal ${NOT_EXPANDED}" {
		t.Errorf("escaped reference = %v, want it kept literal", got)
	}
}

func TestOverrideWinsAndRestIsInherited(t *testing.T) {
	cfg := loadFull(t)
	office := cfg.Instances[1]

	wantRetriever := map[string]any{
		"base_url":   "https://10.0.0.1",
		"verify_tls": true, // overridden: the definition says false
		"api_token":  "unifi-secret",
	}
	if got := office.Retriever.Params; !reflect.DeepEqual(got, wantRetriever) {
		t.Errorf("office retriever params = %v, want %v", got, wantRetriever)
	}

	wantProvider := map[string]any{
		"api_token": "selectel-secret",
		"zone":      "office.com", // overridden
		"rr_name":   "@",          // inherited
		"ttl":       int64(60),    // inherited
	}
	if got := office.Providers[0].Params; !reflect.DeepEqual(got, wantProvider) {
		t.Errorf("office provider params = %v, want %v", got, wantProvider)
	}
}

func TestServiceKeysAreStripped(t *testing.T) {
	cfg := loadFull(t)

	for _, inst := range cfg.Instances {
		for _, plugin := range append([]Plugin{inst.Retriever}, inst.Providers...) {
			for _, key := range []string{"type", "ref"} {
				if _, ok := plugin.Params[key]; ok {
					t.Errorf("instance %q plugin %q: params contain %q", inst.Name, plugin.Ref, key)
				}
			}
		}
	}
}

func TestSameDefinitionReferencedTwice(t *testing.T) {
	cfg := loadFull(t)
	providers := cfg.Instances[0].Providers

	first, second := providers[0], providers[1]
	if first.Ref != "selectel-main" || second.Ref != "selectel-main" {
		t.Fatalf("refs = %q, %q; want both selectel-main", first.Ref, second.Ref)
	}

	if first.Params["zone"] != "homelab.com" || first.Params["rr_name"] != "@" {
		t.Errorf("first reference was affected by the second: %v", first.Params)
	}

	if second.Params["zone"] != "homelab.net" || second.Params["rr_name"] != "sub" {
		t.Errorf("second reference lost its override: %v", second.Params)
	}
}

func TestMergeSharesNoMemory(t *testing.T) {
	newDefinition := func() map[string]any {
		return map[string]any{
			"type":   "selectel",
			"zone":   "a.com",
			"nested": map[string]any{"list": []any{"x"}},
		}
	}
	newOverride := func() map[string]any {
		return map[string]any{
			"ref":   "main",
			"zone":  "b.com",
			"extra": map[string]any{"list": []any{"y"}},
		}
	}

	definition, override := newDefinition(), newOverride()
	pool := map[string]map[string]any{"main": definition}

	plugin, errs := resolvePlugin("provider", "provider #1", pool, override)
	if len(errs) > 0 {
		t.Fatalf("resolvePlugin: %v", errs)
	}

	if !reflect.DeepEqual(definition, newDefinition()) {
		t.Errorf("definition changed: %v", definition)
	}
	if !reflect.DeepEqual(override, newOverride()) {
		t.Errorf("override changed: %v", override)
	}

	// Mutating the result must reach neither input.
	plugin.Params["nested"].(map[string]any)["list"].([]any)[0] = "changed"
	plugin.Params["extra"].(map[string]any)["list"].([]any)[0] = "changed"

	if !reflect.DeepEqual(definition, newDefinition()) {
		t.Errorf("result shares memory with the definition: %v", definition)
	}
	if !reflect.DeepEqual(override, newOverride()) {
		t.Errorf("result shares memory with the override: %v", override)
	}
}

func TestMergeIsShallow(t *testing.T) {
	cfg := mustParse(t, `
[retriever.home]
type = "ifconfigco"

[provider.main]
type = "selectel"
opts = { a = 1, b = 2 }

[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref  = "main"
opts = { a = 9 }
`)

	want := map[string]any{"a": int64(9)}
	if got := cfg.Instances[0].Providers[0].Params["opts"]; !reflect.DeepEqual(got, want) {
		t.Errorf("opts = %v, want the override to replace the table: %v", got, want)
	}
}

func TestIntervals(t *testing.T) {
	cfg := loadFull(t)

	if got := cfg.Instances[0].Interval; got != 30*time.Second {
		t.Errorf("homelab interval = %v, want the instance override 30s", got)
	}

	if got := cfg.Instances[1].Interval; got != DefaultInterval {
		t.Errorf("office interval = %v, want the default %v", got, DefaultInterval)
	}

	custom := mustParse(t, `interval = "1m"`+header+`
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`)
	if custom.Interval != time.Minute || custom.Instances[0].Interval != time.Minute {
		t.Errorf("global interval not inherited: %v / %v", custom.Interval, custom.Instances[0].Interval)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("TOKEN", "tok")
	t.Setenv("EMPTY", "")

	cfg := mustParse(t, `
[retriever.home]
type = "ifconfigco"

[provider.main]
type   = "selectel"
key    = "${TOKEN}"
mixed  = "pre-${TOKEN}-${TOKEN}-post"
empty  = "${EMPTY}"
escape = "$${TOKEN}"
list   = ["${TOKEN}", "plain"]
table  = { inner = "${TOKEN}" }
number = 5

[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`)

	want := map[string]any{
		"key":    "tok",
		"mixed":  "pre-tok-tok-post",
		"empty":  "",
		"escape": "${TOKEN}",
		"list":   []any{"tok", "plain"},
		"table":  map[string]any{"inner": "tok"},
		"number": int64(5),
	}
	if got := cfg.Instances[0].Providers[0].Params; !reflect.DeepEqual(got, want) {
		t.Errorf("params = %v, want %v", got, want)
	}
}

func TestEnvExpansionInOverride(t *testing.T) {
	t.Setenv("ZONE", "b.com")

	cfg := mustParse(t, header+`
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref  = "main"
zone = "${ZONE}"
`)

	if got := cfg.Instances[0].Providers[0].Params["zone"]; got != "b.com" {
		t.Errorf("zone = %v, want b.com", got)
	}
}

func TestStructuralKeysAreNotExpanded(t *testing.T) {
	t.Setenv("NAME", "expanded")

	_, err := Parse([]byte(`
[retriever.home]
type = "ifconfigco"
[provider.main]
type = "selectel"
[[instance]]
name = "${NAME}"
interval = "${NAME}"
[instance.retriever]
ref = "${NAME}"
[[instance.provider]]
ref = "${NAME}"
`))
	if err == nil {
		t.Fatal("Parse succeeded, want errors")
	}

	wantContains(t, err.Error(), `instance "${NAME}"`, `invalid interval "${NAME}"`,
		`ref "${NAME}" is not defined`)
}

func TestArrayOfTablesInParametersIsExpanded(t *testing.T) {
	t.Setenv("TOKEN", "tok")

	cfg := mustParse(t, `
[retriever.home]
type = "ifconfigco"

[provider.main]
type = "selectel"

[[provider.main.zones]]
name = "${TOKEN}"

[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`)

	want := []map[string]any{{"name": "tok"}}
	if got := cfg.Instances[0].Providers[0].Params["zones"]; !reflect.DeepEqual(got, want) {
		t.Errorf("zones = %v, want %v", got, want)
	}
}

func TestUnusedDefinitionDoesNotNeedItsEnv(t *testing.T) {
	mustParse(t, header+`
[provider.unused]
type      = "selectel"
api_token = "${DNSPATCH_TEST_SURELY_UNSET}"

[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`)
}

func TestErrors(t *testing.T) {
	instance := func(body string) string {
		return header + "\n[[instance]]\nname = \"a\"\n" + body
	}

	tests := []struct {
		name  string
		doc   string
		wants []string
	}{
		{
			name: "unknown provider ref lists the defined ones",
			doc: instance(`[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "mian"
`),
			wants: []string{`instance "a"`, `provider #1`, `ref "mian" is not defined`, `defined: main`},
		},
		{
			name: "unknown retriever ref lists the defined ones",
			doc: instance(`[instance.retriever]
ref = "nope"
[[instance.provider]]
ref = "main"
`),
			wants: []string{`instance "a"`, `retriever: ref "nope" is not defined`, `defined: home`},
		},
		{
			name: "no providers are defined at all",
			doc: `
[retriever.home]
type = "ifconfigco"
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`provider #1`, `ref "main" is not defined`, `no providers are defined`},
		},
		{
			name:  "instance without a retriever",
			doc:   instance("[[instance.provider]]\nref = \"main\"\n"),
			wants: []string{`instance "a"`, `retriever is required`},
		},
		{
			name:  "instance without providers",
			doc:   instance("[instance.retriever]\nref = \"home\"\n"),
			wants: []string{`instance "a"`, `at least one provider is required`},
		},
		{
			name: "reference without ref",
			doc: instance(`[instance.retriever]
ref = "home"
[[instance.provider]]
zone = "x.com"
`),
			wants: []string{`instance "a"`, `provider #1`, `"ref" is required`},
		},
		{
			name: "bad instance interval names instance and value",
			doc: header + `
[[instance]]
name = "a"
interval = "5x"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `invalid interval "5x"`},
		},
		{
			name:  "bad global interval",
			doc:   "interval = \"soon\"\n" + header,
			wants: []string{`invalid interval "soon"`},
		},
		{
			name: "interval below the minimum",
			doc: header + `
[[instance]]
name = "a"
interval = "0s"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `interval "0s" is too short`},
		},
		{
			name: "undefined environment variable names instance, plugin and parameter",
			doc: `
[retriever.home]
type = "ifconfigco"
[provider.main]
type = "selectel"
opts = { token = "${DNSPATCH_TEST_SURELY_UNSET}" }
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `provider "main"`, `parameter "opts.token"`,
				`environment variable "DNSPATCH_TEST_SURELY_UNSET" is not set`},
		},
		{
			name: "type cannot be overridden",
			doc: instance(`[instance.retriever]
ref = "home"
[[instance.provider]]
ref  = "main"
type = "other"
`),
			wants: []string{`instance "a"`, `provider "main"`, `"type" cannot be overridden`},
		},
		{
			name: "definition without type",
			doc: `
[provider.main]
zone = "a.com"
`,
			wants: []string{`provider "main"`, `"type" is required`},
		},
		{
			name: "duplicate instance name",
			doc: header + `
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"

[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `used by more than one instance`},
		},
		{
			name:  "instance without a name",
			doc:   header + "\n[[instance]]\n[instance.retriever]\nref = \"home\"\n[[instance.provider]]\nref = \"main\"\n",
			wants: []string{`instance #1`, `"name" is required`},
		},
		{
			name:  "unknown key",
			doc:   "intervall = \"1m\"\n" + header,
			wants: []string{`unknown key "intervall"`},
		},
		{
			name:  "instances typed as a table of names",
			doc:   header + "\n[instances.a]\nname = \"a\"\n",
			wants: []string{`unknown key "instances"`, `instance`},
		},
		{
			name: "unknown key in the second instance names that instance",
			doc: header + `
[[instance]]
name = "first"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"

[[instance]]
name = "second"
interva = "1m"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "second"`, `unknown key "interva"`},
		},
		{
			name: "misspelled instance table",
			doc: instance(`[instance.retreiver]
ref = "home"
[[instance.provider]]
ref = "main"
`),
			wants: []string{`instance "a"`, `unknown key "retreiver"`, `retriever is required`},
		},
		{
			name:  "definition written as an array of tables",
			doc:   "[[provider.main]]\ntype = \"selectel\"\n",
			wants: []string{`provider "main" must be a table`},
		},
		{
			name:  "pool written as a scalar",
			doc:   "provider = 5\n",
			wants: []string{`"provider" must be a table of named definitions`},
		},
		{
			name: "instance retriever written as a string",
			doc: header + `
[[instance]]
name = "a"
retriever = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `"retriever" must be a table`},
		},
		{
			name: "instance retriever written as an array of tables",
			doc: header + `
[[instance]]
name = "a"
[[instance.retriever]]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `"retriever" must be a table`},
		},
		{
			name: "instance provider written as a table",
			doc: header + `
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[instance.provider]
ref = "main"
`,
			wants: []string{`instance "a"`, `"provider" must be an array of tables`},
		},
		{
			name:  "instance written as a single table",
			doc:   header + "\n[instance]\nname = \"a\"\n",
			wants: []string{`"instance" must be an array of tables`},
		},
		{
			name: "interval of the wrong type",
			doc: header + `
[[instance]]
name = "a"
interval = 30
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `"interval" must be a string`},
		},
		{
			name:  "no instances",
			doc:   header,
			wants: []string{`no instances defined`},
		},
		{
			name: "ref inside a definition",
			doc: `
[provider.main]
type = "selectel"
ref  = "other"
`,
			wants: []string{`provider "main"`, `"ref" is only valid in an instance`},
		},
		{
			name: "malformed environment references",
			doc: `
[retriever.home]
type = "ifconfigco"
[provider.main]
type = "selectel"
a = "${A-B}"
b = "${TOKEN"
c = "${}"
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`instance "a"`, `provider "main"`, `parameter "a"`, `parameter "b"`, `parameter "c"`,
				`malformed environment reference`},
		},
		{
			name: "every missing variable is reported",
			doc: `
[retriever.home]
type = "ifconfigco"
[provider.main]
type = "selectel"
a = "${DNSPATCH_TEST_UNSET_ONE}"
b = ["x", "${DNSPATCH_TEST_UNSET_TWO}"]
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "main"
`,
			wants: []string{`parameter "a"`, `DNSPATCH_TEST_UNSET_ONE`, `parameter "b[1]"`, `DNSPATCH_TEST_UNSET_TWO`},
		},
		{
			name: "defined names are sorted",
			doc: `
[retriever.home]
type = "ifconfigco"
[provider.zeta]
type = "selectel"
[provider.alpha]
type = "selectel"
[[instance]]
name = "a"
[instance.retriever]
ref = "home"
[[instance.provider]]
ref = "gone"
`,
			wants: []string{`defined: alpha, zeta`},
		},
		{
			name:  "syntax error",
			doc:   "interval = \n",
			wants: []string{`line 1`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantContains(t, parseError(t, tt.doc), tt.wants...)
		})
	}
}

func TestAllErrorsAreReported(t *testing.T) {
	msg := parseError(t, header+`
[[instance]]
name = "a"
interval = "bad"
[[instance.provider]]
ref = "gone"
`)

	wantContains(t, msg, `invalid interval "bad"`, `retriever is required`, `ref "gone" is not defined`)
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil {
		t.Fatal("Load succeeded for a missing file")
	}

	wantContains(t, err.Error(), "reading config", "missing.toml")
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}

	flagPath, envPath, defaultPath := write("flag.toml"), write("env.toml"), write("default.toml")
	missing := filepath.Join(dir, "missing.toml")
	defaults := []string{filepath.Join(dir, "absent.toml"), defaultPath}

	tests := []struct {
		name      string
		flag, env string
		defaults  []string
		want      string
		wantErr   []string
	}{
		{name: "flag beats env and defaults", flag: flagPath, env: envPath, defaults: defaults, want: flagPath},
		{name: "env beats defaults", env: envPath, defaults: defaults, want: envPath},
		{name: "first existing default", defaults: defaults, want: defaultPath},
		{
			name: "missing flag path is not replaced by env", flag: missing, env: envPath, defaults: defaults,
			wantErr: []string{"--config", "missing.toml"},
		},
		{
			name: "missing env path is not replaced by defaults", env: missing, defaults: defaults,
			wantErr: []string{EnvPath, "missing.toml"},
		},
		{
			name: "nothing found lists the candidates", defaults: []string{"a.toml", "b.toml"},
			wantErr: []string{"no config file found", "a.toml, b.toml", EnvPath},
		},
		{name: "a directory is not a default config", defaults: []string{dir}, wantErr: []string{"no config file found"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePath(tt.flag, tt.env, tt.defaults)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("resolvePath = %q, want an error", got)
				}
				wantContains(t, err.Error(), tt.wantErr...)

				return
			}

			if err != nil {
				t.Fatalf("resolvePath: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolvePath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePathReadsEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPath, path)

	got, err := ResolvePath("")
	if err != nil || got != path {
		t.Errorf("ResolvePath = %q, %v; want %q", got, err, path)
	}
}

func TestProxyParameterExpandedAndOverridden(t *testing.T) {
	t.Setenv("REGRU_USERNAME", "user")
	t.Setenv("REGRU_PASSWORD", "pass")
	t.Setenv("PROXY_URL", "socks5://proxyuser:pr0xy@203.0.113.5:1080")

	cfg, err := Load(filepath.Join("testdata", "proxy.toml"))
	if err != nil {
		t.Fatalf("Load(proxy.toml): %v", err)
	}

	want := []string{"socks5://proxyuser:pr0xy@203.0.113.5:1080", "https://proxy.example.net:8443", ""}

	providers := cfg.Instances[0].Providers
	if len(providers) != len(want) {
		t.Fatalf("providers = %d, want %d", len(providers), len(want))
	}
	for i, p := range providers {
		if p.Params["proxy"] != want[i] {
			t.Errorf("provider %d: proxy = %q, want %q", i, p.Params["proxy"], want[i])
		}
	}

	if _, ok := cfg.Instances[0].Retriever.Params["proxy"]; ok {
		t.Error("the retriever must not receive a proxy parameter")
	}
}
