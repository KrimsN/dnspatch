package plugin_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

type nested struct {
	Enabled bool   `toml:"enabled"`
	Label   string `toml:"label" default:"none"`
}

type decodeConfig struct {
	APIToken  string            `toml:"api_token,required"`
	Port      int               `toml:"port" default:"443"`
	Ratio     float64           `toml:"ratio"`
	VerifyTLS bool              `toml:"verify_tls" default:"true"`
	Timeout   time.Duration     `toml:"timeout" default:"10s"`
	Bind      netip.Addr        `toml:"bind"`
	Domains   []string          `toml:"domains"`
	Headers   map[string]string `toml:"headers"`
	Nested    nested            `toml:"nested"`
	Internal  string            `toml:"-"`
	Untagged  string
}

func TestDecodeFillsFields(t *testing.T) {
	cfg, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"port":      int64(8443),
		"ratio":     int64(2),
		"bind":      "192.0.2.1",
		"domains":   []any{"a.example", "b.example"},
		"headers":   map[string]any{"X-Trace": "on"},
		"nested":    map[string]any{"enabled": true},
		"untagged":  "by field name",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.APIToken != "secret" {
		t.Errorf("APIToken = %q, want %q", cfg.APIToken, "secret")
	}
	if cfg.Port != 8443 {
		t.Errorf("Port = %d, want 8443", cfg.Port)
	}
	if cfg.Ratio != 2 {
		t.Errorf("Ratio = %v, want 2", cfg.Ratio)
	}
	if cfg.Bind != netip.MustParseAddr("192.0.2.1") {
		t.Errorf("Bind = %v, want 192.0.2.1", cfg.Bind)
	}
	if len(cfg.Domains) != 2 || cfg.Domains[1] != "b.example" {
		t.Errorf("Domains = %v, want [a.example b.example]", cfg.Domains)
	}
	if cfg.Headers["X-Trace"] != "on" {
		t.Errorf("Headers = %v, want X-Trace=on", cfg.Headers)
	}
	if !cfg.Nested.Enabled || cfg.Nested.Label != "none" {
		t.Errorf("Nested = %+v, want {true none}", cfg.Nested)
	}
	if cfg.Untagged != "by field name" {
		t.Errorf("Untagged = %q, want %q", cfg.Untagged, "by field name")
	}
}

func TestDecodeAppliesDefaults(t *testing.T) {
	cfg, err := plugin.Decode[decodeConfig](map[string]any{"api_token": "secret"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Port != 443 {
		t.Errorf("Port = %d, want the default 443", cfg.Port)
	}
	if !cfg.VerifyTLS {
		t.Error("VerifyTLS = false, want the default true")
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want the default 10s", cfg.Timeout)
	}
}

func TestDecodeExplicitValueOverridesDefault(t *testing.T) {
	cfg, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token":  "secret",
		"port":       int64(80),
		"verify_tls": false,
		"timeout":    "45s",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Port != 80 {
		t.Errorf("Port = %d, want 80", cfg.Port)
	}
	if cfg.VerifyTLS {
		t.Error("VerifyTLS = true, want the explicit false")
	}
	if cfg.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s", cfg.Timeout)
	}
}

func TestDecodeRequiredParameterMissing(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{"port": int64(80)})
	if err == nil {
		t.Fatal("Decode succeeded, want an error about the missing parameter")
	}

	if !strings.Contains(err.Error(), `parameter "api_token" is required`) {
		t.Errorf("error = %q, want it to name api_token", err)
	}
}

func TestDecodeUnknownParameterSuggestsClosest(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"api_tokn":  "secret",
	})
	if err == nil {
		t.Fatal("Decode succeeded, want an error about the unknown parameter")
	}

	want := `unknown parameter "api_tokn" (did you mean "api_token"?)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestDecodeUnknownParameterListsKnown(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"zzzzzzzz":  "value",
	})
	if err == nil {
		t.Fatal("Decode succeeded, want an error about the unknown parameter")
	}

	message := err.Error()
	if !strings.Contains(message, `unknown parameter "zzzzzzzz" (known parameters:`) {
		t.Errorf("error = %q, want it to list the known parameters", err)
	}
	if !strings.Contains(message, "api_token") {
		t.Errorf("error = %q, want the list to contain api_token", err)
	}
}

func TestDecodeExcludedFieldIsUnknown(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"internal":  "value",
	})
	if err == nil {
		t.Fatal("Decode succeeded, want a field tagged \"-\" to be unknown")
	}
}

func TestDecodeReportsEveryProblemAtOnce(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{"zzzzzzzz": "value"})
	if err == nil {
		t.Fatal("Decode succeeded, want errors")
	}

	message := err.Error()
	if !strings.Contains(message, "is required") || !strings.Contains(message, "unknown parameter") {
		t.Errorf("error = %q, want both the missing and the unknown parameter", err)
	}
}

func TestDecodeTypeMismatch(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"port":      "8443",
	})
	if err == nil {
		t.Fatal("Decode succeeded, want a type error")
	}

	want := `parameter "port": cannot assign string to int`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestDecodeIntegerOutOfRange(t *testing.T) {
	type small struct {
		Port int8 `toml:"port"`
	}

	_, err := plugin.Decode[small](map[string]any{"port": int64(1024)})
	if err == nil {
		t.Fatal("Decode succeeded, want a range error")
	}

	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error = %q, want it to mention the range", err)
	}
}

func TestDecodeInvalidAddress(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"bind":      "<html>not an address</html>",
	})
	if err == nil {
		t.Fatal("Decode succeeded, want the address to be rejected")
	}

	if !strings.Contains(err.Error(), `parameter "bind"`) {
		t.Errorf("error = %q, want it to name bind", err)
	}
}

func TestDecodeNestedParameterErrorIsQualified(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"nested":    map[string]any{"enabled": "yes"},
	})
	if err == nil {
		t.Fatal("Decode succeeded, want a type error")
	}

	if !strings.Contains(err.Error(), `parameter "nested.enabled"`) {
		t.Errorf("error = %q, want the nested parameter path", err)
	}
}

func TestDecodeEmptyConfigRejectsAnyParameter(t *testing.T) {
	type empty struct{}

	_, err := plugin.Decode[empty](map[string]any{"anything": 1})
	if err == nil {
		t.Fatal("Decode succeeded, want an error")
	}

	if !strings.Contains(err.Error(), "takes no parameters") {
		t.Errorf("error = %q, want it to say the plugin takes no parameters", err)
	}
}

func TestDecodeRejectsNonStruct(t *testing.T) {
	_, err := plugin.Decode[string](nil)
	if err == nil {
		t.Fatal("Decode succeeded, want a struct to be required")
	}
}

type nestedRequired struct {
	Must  string `toml:"must,required"`
	Label string `toml:"label" default:"dflt"`
}

type tables struct {
	Block    nestedRequired  `toml:"block"`
	Optional *nestedRequired `toml:"optional"`
}

func TestDecodeValidatesOmittedNestedTable(t *testing.T) {
	_, err := plugin.Decode[tables](map[string]any{})
	if err == nil {
		t.Fatal("Decode succeeded, want the required field of the omitted table to be reported")
	}

	if !strings.Contains(err.Error(), `parameter "block.must" is required`) {
		t.Errorf("error = %q, want it to name block.must", err)
	}
}

func TestDecodeAppliesDefaultsOfOmittedNestedTable(t *testing.T) {
	cfg, err := plugin.Decode[tables](map[string]any{
		"block": map[string]any{"must": "value"},
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Block.Label != "dflt" {
		t.Errorf("Block.Label = %q, want the default %q", cfg.Block.Label, "dflt")
	}
}

func TestDecodeOptionalBlockStaysNil(t *testing.T) {
	cfg, err := plugin.Decode[tables](map[string]any{
		"block": map[string]any{"must": "value"},
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Optional != nil {
		t.Errorf("Optional = %+v, want nil for an omitted pointer block", cfg.Optional)
	}
}

func TestDecodeNestedUnknownParameterSuggestsClosest(t *testing.T) {
	_, err := plugin.Decode[tables](map[string]any{
		"block": map[string]any{"must": "value", "labl": "typo"},
	})
	if err == nil {
		t.Fatal("Decode succeeded, want the nested typo to be reported")
	}

	want := `unknown parameter "block.labl" (did you mean "block.label"?)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

func TestDecodeConflictingParameterCase(t *testing.T) {
	_, err := plugin.Decode[decodeConfig](map[string]any{
		"api_token": "secret",
		"API_TOKEN": "secret",
	})
	if err == nil {
		t.Fatal("Decode succeeded, want names differing only in case to conflict")
	}

	want := `parameters "API_TOKEN" and "api_token" both set "api_token"`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

type sharedBase struct {
	Timeout time.Duration `toml:"timeout" default:"5s"`
	Region  string        `toml:"region,required"`
}

type promoted struct {
	sharedBase
	Zone string `toml:"zone"`
}

func TestDecodePromotesEmbeddedFields(t *testing.T) {
	cfg, err := plugin.Decode[promoted](map[string]any{
		"region": "eu",
		"zone":   "a",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Region != "eu" {
		t.Errorf("Region = %q, want %q", cfg.Region, "eu")
	}
	if cfg.Zone != "a" {
		t.Errorf("Zone = %q, want %q", cfg.Zone, "a")
	}
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want the default 5s", cfg.Timeout)
	}
}

func TestDecodeRequiredFieldOfEmbeddedStruct(t *testing.T) {
	_, err := plugin.Decode[promoted](map[string]any{"zone": "a"})
	if err == nil {
		t.Fatal("Decode succeeded, want the promoted required field to be reported")
	}

	if !strings.Contains(err.Error(), `parameter "region" is required`) {
		t.Errorf("error = %q, want it to name region", err)
	}
}

type duplicated struct {
	First  string `toml:"same"`
	Second string `toml:"same"`
}

func TestDecodeDuplicateParameterNamePanics(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("Decode did not panic on two fields taking the same parameter")
		}
		if !strings.Contains(fmt.Sprint(recovered), `First and Second`) {
			t.Errorf("panic = %v, want it to name both fields", recovered)
		}
	}()

	_, _ = plugin.Decode[duplicated](map[string]any{"same": "value"})
}

type brokenDefault struct {
	Port int `toml:"port" default:"many"`
}

func TestDecodeSkipsDefaultWhenValueIsGiven(t *testing.T) {
	cfg, err := plugin.Decode[brokenDefault](map[string]any{"port": int64(80)})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if cfg.Port != 80 {
		t.Errorf("Port = %d, want the explicit 80", cfg.Port)
	}
}

func TestDecodeReportsBrokenDefaultWhenValueIsMissing(t *testing.T) {
	_, err := plugin.Decode[brokenDefault](nil)
	if err == nil || !strings.Contains(err.Error(), `invalid default for parameter "port"`) {
		t.Errorf("error = %v, want it to report the invalid default", err)
	}
}

type loose struct {
	List  []any          `toml:"list"`
	Table map[string]any `toml:"table"`
}

func TestDecodeDoesNotShareMemoryWithParams(t *testing.T) {
	params := map[string]any{
		"list":  []any{"a", []any{"inner"}},
		"table": map[string]any{"key": "value", "sub": map[string]any{"deep": "value"}},
	}

	cfg, err := plugin.Decode[loose](params)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Changing what the caller passed in, at every depth, must not reach the
	// decoded configuration.
	list := params["list"].([]any)
	list[0] = "changed"
	list[1].([]any)[0] = "changed"

	table := params["table"].(map[string]any)
	table["key"] = "changed"
	table["sub"].(map[string]any)["deep"] = "changed"

	if cfg.List[0] != "a" || cfg.List[1].([]any)[0] != "inner" {
		t.Errorf("List = %v, want it unaffected by the caller's changes", cfg.List)
	}
	if cfg.Table["key"] != "value" || cfg.Table["sub"].(map[string]any)["deep"] != "value" {
		t.Errorf("Table = %v, want it unaffected by the caller's changes", cfg.Table)
	}
}
