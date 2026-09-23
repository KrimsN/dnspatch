// Package config parses the TOML configuration: named retriever and provider
// definitions, instances and their polling intervals.
//
// A file holds pools of named definitions ([retriever.<name>] and
// [provider.<name>], each with a "type") and a list of [[instance]] tables.
// An instance refers to definitions by "ref" and may override any of their
// parameters. Parse resolves every reference, so the result holds, per
// instance, the plugin type and the final parameters ready for the plugin
// registry.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultInterval is used when neither the file nor the instance sets one.
	DefaultInterval = 5 * time.Minute

	// MinInterval is the shortest accepted polling interval.
	MinInterval = time.Second

	// EnvPath names the environment variable that holds the config file path.
	EnvPath = "DNSPATCH_CONFIG"
)

// defaultPaths are tried in order when no path is given explicitly.
var defaultPaths = []string{"./dnspatch.toml", "/etc/dnspatch/config.toml"}

var errIntervalType = errors.New(`"interval" must be a string such as "30s" or "5m"`)

// Config is a parsed and fully resolved configuration.
type Config struct {
	// Interval is the polling interval instances fall back to.
	Interval  time.Duration
	Instances []Instance
}

// Instance ties one or more retrievers to one or more providers. There is at
// most one retriever per address family: two retrievers means one for IPv4
// and one for IPv6, distinguished at run time by the address each returns.
type Instance struct {
	Name string
	// Interval is the polling interval, already resolved against the global one.
	Interval   time.Duration
	Retrievers []Plugin
	Providers  []Plugin
}

// Plugin is a plugin type together with its final parameters: the named
// definition overlaid with the instance override. The service keys "type"
// and "ref" are not part of Params, and environment references are expanded.
type Plugin struct {
	// Ref is the name of the definition the plugin was built from.
	Ref    string
	Type   string
	Params map[string]any
}

// rawInstance is one [[instance]] table after its shape has been checked.
type rawInstance struct {
	Name       string
	Interval   *string
	Retrievers []map[string]any
	Providers  []map[string]any
}

// Load reads and parses the config file at path. Problems are listed under a
// header line naming the file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("config %s:\n%w", path, err)
	}

	return cfg, nil
}

// Parse parses TOML config data. Problems are reported together, one per
// line, each naming the instance, plugin and parameter it belongs to.
func Parse(data []byte) (Config, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return Config{}, err
	}

	retrievers, providers, tables, shapeErrs := checkShape(doc)
	if len(shapeErrs) > 0 {
		return Config{}, errors.Join(shapeErrs...)
	}

	var errs []error

	global := DefaultInterval
	if value, ok := doc["interval"]; ok {
		interval, err := parseInterval(value)
		if err != nil {
			errs = append(errs, err)
		} else {
			global = interval
		}
	}

	if len(tables) == 0 {
		errs = append(errs, errors.New("no instances defined"))
	}

	cfg := Config{Interval: global}
	seen := make(map[string]bool, len(tables))

	for i, table := range tables {
		in, instErrs := checkInstance(table)
		label := instanceLabel(i, in.Name)

		if in.Name != "" && seen[in.Name] {
			instErrs = append(instErrs, errors.New("name is used by more than one instance"))
		}
		seen[in.Name] = true

		resolved, resolveErrs := resolveInstance(in, retrievers, providers, global)
		instErrs = append(instErrs, resolveErrs...)

		for _, e := range instErrs {
			errs = append(errs, fmt.Errorf("%s: %w", label, e))
		}
		cfg.Instances = append(cfg.Instances, resolved)
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}

	return cfg, nil
}

// ResolvePath picks the config file path. Priority: the flag value, the
// DNSPATCH_CONFIG variable, then the first existing default location. A path
// given explicitly must exist; the defaults are not consulted in that case.
func ResolvePath(flagValue string) (string, error) {
	return resolvePath(flagValue, os.Getenv(EnvPath), defaultPaths)
}

func resolvePath(flagValue, envValue string, defaults []string) (string, error) {
	explicit, source := flagValue, "--config"
	if explicit == "" {
		explicit, source = envValue, EnvPath
	}

	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("%s: %w", source, err)
		}

		return explicit, nil
	}

	for _, path := range defaults {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	return "", fmt.Errorf("no config file found: pass --config, set %s or create one of: %s",
		EnvPath, strings.Join(defaults, ", "))
}

// checkShape verifies the top-level layout of the document: known keys only,
// pools of named tables, an array of instance tables. Definitions are checked
// for a type.
func checkShape(doc map[string]any) (retrievers, providers map[string]map[string]any, instances []map[string]any, errs []error) {
	errs = unknownKeys(doc, "interval", "retriever", "provider", "instance")

	var poolErrs []error

	retrievers, poolErrs = checkPool("retriever", doc["retriever"])
	errs = append(errs, poolErrs...)

	providers, poolErrs = checkPool("provider", doc["provider"])
	errs = append(errs, poolErrs...)

	if value, ok := doc["instance"]; ok {
		if instances, ok = asTables(value); !ok {
			errs = append(errs, errors.New(`"instance" must be an array of tables: [[instance]]`))
		}
	}

	return retrievers, providers, instances, errs
}

// checkPool verifies that a pool is a table of tables and that every
// definition names its type.
func checkPool(kind string, value any) (map[string]map[string]any, []error) {
	if value == nil {
		return nil, nil
	}

	table, ok := value.(map[string]any)
	if !ok {
		return nil, []error{fmt.Errorf("%q must be a table of named definitions: [%s.<name>]", kind, kind)}
	}

	pool := make(map[string]map[string]any, len(table))

	var errs []error

	for _, name := range slices.Sorted(maps.Keys(table)) {
		definition, ok := table[name].(map[string]any)
		if !ok {
			errs = append(errs, fmt.Errorf("%s %q must be a table: [%s.%s]", kind, name, kind, name))
			continue
		}

		if typ, _ := definition["type"].(string); typ == "" {
			errs = append(errs, fmt.Errorf(`%s %q: "type" is required and must be a string`, kind, name))
		}

		if _, ok := definition["ref"]; ok {
			errs = append(errs, fmt.Errorf(`%s %q: "ref" is only valid in an instance`, kind, name))
		}

		pool[name] = definition
	}

	return pool, errs
}

// checkInstance verifies the layout of one instance table: known keys and
// value types.
func checkInstance(table map[string]any) (rawInstance, []error) {
	errs := unknownKeys(table, "name", "interval", "retriever", "provider")

	var in rawInstance

	if value, ok := table["name"]; ok {
		if in.Name, ok = value.(string); !ok {
			errs = append(errs, errors.New(`"name" must be a string`))
		}
	}

	if value, ok := table["interval"]; ok {
		text, isString := value.(string)
		if !isString {
			errs = append(errs, errIntervalType)
		} else {
			in.Interval = &text
		}
	}

	if value, ok := table["retriever"]; ok {
		if in.Retrievers, ok = asTables(value); !ok {
			errs = append(errs, errors.New(`"retriever" must be an array of tables: [[instance.retriever]]`))
		}
	}

	if value, ok := table["provider"]; ok {
		if in.Providers, ok = asTables(value); !ok {
			errs = append(errs, errors.New(`"provider" must be an array of tables: [[instance.provider]]`))
		}
	}

	return in, errs
}

// resolveInstance validates one instance and resolves its references.
func resolveInstance(in rawInstance, retrievers, providers map[string]map[string]any, global time.Duration) (Instance, []error) {
	inst := Instance{Name: in.Name, Interval: global}

	var errs []error

	if in.Name == "" {
		errs = append(errs, errors.New(`"name" is required`))
	}

	if in.Interval != nil {
		interval, err := parseInterval(*in.Interval)
		if err != nil {
			errs = append(errs, err)
		} else {
			inst.Interval = interval
		}
	}

	switch {
	case len(in.Retrievers) == 0:
		errs = append(errs, errors.New("at least one retriever is required"))
	case len(in.Retrievers) > 2:
		errs = append(errs, errors.New("at most two retrievers are supported (one per address family)"))
	}

	for i, override := range in.Retrievers {
		plugin, pluginErrs := resolvePlugin("retriever", fmt.Sprintf("retriever #%d", i+1), retrievers, override)
		errs = append(errs, pluginErrs...)
		inst.Retrievers = append(inst.Retrievers, plugin)
	}

	errs = append(errs, duplicateRetrieverFamilies(inst.Retrievers)...)

	if len(in.Providers) == 0 {
		errs = append(errs, errors.New("at least one provider is required"))
	}

	for i, override := range in.Providers {
		plugin, pluginErrs := resolvePlugin("provider", fmt.Sprintf("provider #%d", i+1), providers, override)
		errs = append(errs, pluginErrs...)
		inst.Providers = append(inst.Providers, plugin)
	}

	errs = append(errs, duplicateProviders(inst.Providers)...)

	return inst, errs
}

// parseInterval parses a duration such as "30s" or "5m" of at least MinInterval.
func parseInterval(value any) (time.Duration, error) {
	text, ok := value.(string)
	if !ok {
		return 0, errIntervalType
	}

	interval, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q: %w", text, err)
	}

	if interval < MinInterval {
		return 0, fmt.Errorf("interval %q is too short: the minimum is %s", text, MinInterval)
	}

	return interval, nil
}

// unknownKeys reports every key of table that is not in allowed.
func unknownKeys(table map[string]any, allowed ...string) []error {
	var errs []error

	for _, key := range slices.Sorted(maps.Keys(table)) {
		if !slices.Contains(allowed, key) {
			errs = append(errs, fmt.Errorf("unknown key %q (expected one of: %s)",
				key, strings.Join(slices.Sorted(slices.Values(allowed)), ", ")))
		}
	}

	return errs
}

// asTables converts an array of tables. The decoder yields []map[string]any
// for [[name]] and []any for an inline array.
func asTables(value any) ([]map[string]any, bool) {
	switch v := value.(type) {
	case []map[string]any:
		return v, true
	case []any:
		tables := make([]map[string]any, len(v))
		for i, item := range v {
			table, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			tables[i] = table
		}

		return tables, true
	default:
		return nil, false
	}
}

func instanceLabel(index int, name string) string {
	if name == "" {
		return fmt.Sprintf("instance #%d", index+1)
	}

	return fmt.Sprintf("instance %q", name)
}

// definedNames renders the names in a pool for an error message.
func definedNames(kind string, pool map[string]map[string]any) string {
	if len(pool) == 0 {
		return fmt.Sprintf("no %ss are defined", kind)
	}

	return "defined: " + strings.Join(slices.Sorted(maps.Keys(pool)), ", ")
}

// duplicateProviders reports providers of one instance that were built from
// the same definition and ended up with the same parameters: they would write
// the same record twice on every tick. A provider that overrides a parameter,
// such as the zone, is a different one.
func duplicateProviders(providers []Plugin) []error {
	var errs []error

	for i, later := range providers {
		if later.Ref == "" {
			continue
		}

		for j, earlier := range providers[:i] {
			if earlier.Ref == later.Ref && reflect.DeepEqual(earlier.Params, later.Params) {
				errs = append(errs, fmt.Errorf("provider #%d repeats provider #%d: same ref %q and same parameters, the record would be written twice",
					i+1, j+1, later.Ref))
				break
			}
		}
	}

	return errs
}

// duplicateRetrieverFamilies reports two retrievers declared for the same
// address family via their "family" parameter. This is an early hint for a
// likely misconfiguration, not a guarantee: the actual family is only known
// once a retriever returns an address at run time.
func duplicateRetrieverFamilies(retrievers []Plugin) []error {
	if len(retrievers) != 2 {
		return nil
	}

	family := func(p Plugin) (string, bool) {
		f, ok := p.Params["family"].(string)
		return f, ok && f != ""
	}

	f1, ok1 := family(retrievers[0])
	f2, ok2 := family(retrievers[1])

	if ok1 && ok2 && f1 == f2 {
		return []error{fmt.Errorf("retriever #1 and #2 are both configured for family %q", f1)}
	}

	return nil
}
