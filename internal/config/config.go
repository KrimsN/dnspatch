// Package config parses the TOML configuration: named retriever, provider and
// notifier definitions, instances and their polling intervals.
//
// A file holds pools of named definitions ([retriever.<name>],
// [provider.<name>] and [notify.<name>], each with a "type") and a list of
// [[instance]] tables. An instance refers to retriever and provider
// definitions by "ref" and may override any of their parameters, and lists the
// notifiers it publishes to by name. Parse resolves every reference, so the
// result holds, per instance, the plugin type and the final parameters ready
// for the plugin registry.
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
	// Notify holds the notifiers, by definition name, that at least one
	// instance publishes to: the brokers (for example Redis) that receive an
	// instance's success/failure transitions. A build that has no notifier
	// rejects a config that uses one, the same way an unsupported PingURL is
	// rejected. Each entry is one broker connection, shared by every instance
	// that lists it; two of them may be of one type, for example two Redis
	// servers. A definition no instance uses is not resolved.
	Notify map[string]Plugin
}

// Instance ties one or more retrievers to one or more providers. Retrievers
// are polled in order until every address family is filled: a later one
// reporting a family an earlier one already filled is a redundant fallback
// source, not an error.
type Instance struct {
	Name string
	// Interval is the polling interval, already resolved against the global one.
	Interval   time.Duration
	Retrievers []Plugin
	Providers  []Plugin
	// PingURL, when set, is called on every completed cycle of this instance
	// by a build that supports monitoring hooks (the ping build tag); a build
	// that does not rejects a config that sets it rather than silently
	// ignoring it. Environment and file references ("${NAME}", "${file:PATH}")
	// are expanded, same as a plugin parameter, since the URL commonly embeds
	// a secret token (Healthchecks.io, Uptime Kuma).
	PingURL string
	// Notify names the notifiers this instance publishes to, each a key of
	// Config.Notify. An instance that does not say gets all the notifiers the
	// file defines, and one that says "notify = []" has none.
	Notify []string
}

// Plugin is a plugin type together with its final parameters: the named
// definition overlaid with the instance override. The service keys "type"
// and "ref" are not part of Params, and environment references are expanded.
type Plugin struct {
	// Ref is the name of the definition the plugin was built from, or empty
	// for a plugin declared inline with "type" instead of "ref".
	Ref    string
	Type   string
	Params map[string]any
}

// Name is how messages and logs call the plugin: the name of the definition it
// was built from, or its type for a plugin declared inline.
func (p Plugin) Name() string {
	if p.Ref != "" {
		return p.Ref
	}

	return p.Type
}

// rawInstance is one [[instance]] table after its shape has been checked.
type rawInstance struct {
	Name       string
	Interval   *string
	PingURL    *string
	Retrievers []map[string]any
	Providers  []map[string]any
	// Notify is nil when the instance does not set "notify", so that an empty
	// list can be told from a missing one.
	Notify []string
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

	retrievers, providers, notifiers, tables, shapeErrs := checkShape(doc)
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

	used := make(map[string]bool)

	seen := make(map[string]bool, len(tables))

	for i, table := range tables {
		in, instErrs := checkInstance(table)
		label := instanceLabel(i, in.Name)

		if in.Name != "" && seen[in.Name] {
			instErrs = append(instErrs, errors.New("name is used by more than one instance"))
		}
		seen[in.Name] = true

		resolved, resolveErrs := resolveInstance(in, retrievers, providers, notifiers, global)
		instErrs = append(instErrs, resolveErrs...)

		for _, name := range resolved.Notify {
			used[name] = true
		}

		for _, e := range instErrs {
			errs = append(errs, fmt.Errorf("%s: %w", label, e))
		}
		cfg.Instances = append(cfg.Instances, resolved)
	}

	// Only what an instance publishes to is resolved: the environment
	// references of a definition nobody uses need not be set.
	for _, name := range slices.Sorted(maps.Keys(used)) {
		if !hasKey(notifiers, name) {
			continue
		}

		notify, notifyErrs := resolvePluginByRef("notify", "notify", notifiers, name, nil)
		errs = append(errs, notifyErrs...)

		if cfg.Notify == nil {
			cfg.Notify = make(map[string]Plugin, len(used))
		}

		cfg.Notify[name] = notify
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
// pools of named tables, and an array of instance tables. Definitions are
// checked for a type.
func checkShape(doc map[string]any) (retrievers, providers, notifiers map[string]map[string]any, instances []map[string]any, errs []error) {
	errs = unknownKeys(doc, "interval", "retriever", "provider", "instance", "notify")

	var poolErrs []error

	retrievers, poolErrs = checkPool("retriever", doc["retriever"])
	errs = append(errs, poolErrs...)

	providers, poolErrs = checkPool("provider", doc["provider"])
	errs = append(errs, poolErrs...)

	notifiers, poolErrs = checkPool("notify", doc["notify"])
	errs = append(errs, poolErrs...)

	if value, ok := doc["instance"]; ok {
		if instances, ok = asTables(value); !ok {
			errs = append(errs, errors.New(`"instance" must be an array of tables: [[instance]]`))
		}
	}

	return retrievers, providers, notifiers, instances, errs
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
	errs := unknownKeys(table, "name", "interval", "ping_url", "retriever", "provider", "notify")

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

	if value, ok := table["ping_url"]; ok {
		text, isString := value.(string)
		if !isString {
			errs = append(errs, errors.New(`"ping_url" must be a string`))
		} else {
			in.PingURL = &text
		}
	}

	if value, ok := table["retriever"]; ok {
		var refErrs []error
		in.Retrievers, refErrs = asRefs("retriever", value)
		errs = append(errs, refErrs...)
	}

	if value, ok := table["provider"]; ok {
		var refErrs []error
		in.Providers, refErrs = asRefs("provider", value)
		errs = append(errs, refErrs...)
	}

	if value, ok := table["notify"]; ok {
		if in.Notify, ok = asStrings(value); !ok {
			errs = append(errs, errors.New(`"notify" must be an array of names of [notify.<name>] definitions, for example ["alerts"]`))
		}
	}

	return in, errs
}

// resolveInstance validates one instance and resolves its references.
func resolveInstance(in rawInstance, retrievers, providers, notifiers map[string]map[string]any, global time.Duration) (Instance, []error) {
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

	if in.PingURL != nil {
		pingURL, err := expandString(*in.PingURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q: %w", "ping_url", err))
		} else {
			inst.PingURL = pingURL
		}
	}

	if len(in.Retrievers) == 0 {
		errs = append(errs, errors.New("at least one retriever is required"))
	}

	for i, override := range in.Retrievers {
		plugin, pluginErrs := resolvePlugin("retriever", fmt.Sprintf("retriever #%d", i+1), retrievers, override)
		errs = append(errs, pluginErrs...)
		inst.Retrievers = append(inst.Retrievers, plugin)
	}

	if len(in.Providers) == 0 {
		errs = append(errs, errors.New("at least one provider is required"))
	}

	for i, override := range in.Providers {
		plugin, pluginErrs := resolvePlugin("provider", fmt.Sprintf("provider #%d", i+1), providers, override)
		errs = append(errs, pluginErrs...)
		inst.Providers = append(inst.Providers, plugin)
	}

	errs = append(errs, duplicateProviders(inst.Providers)...)

	notify, notifyErrs := resolveNotify(in.Notify, notifiers)
	errs = append(errs, notifyErrs...)
	inst.Notify = notify

	return inst, errs
}

// resolveNotify picks the notifiers of an instance from the names it lists:
// every one it names must be defined, and none twice. An instance that lists
// none at all, as opposed to an empty list, publishes to every definition.
func resolveNotify(names []string, notifiers map[string]map[string]any) ([]string, []error) {
	if names == nil {
		return slices.Sorted(maps.Keys(notifiers)), nil
	}

	var errs []error

	for i, name := range names {
		switch {
		case !hasKey(notifiers, name):
			errs = append(errs, fmt.Errorf("notify %q is not defined (%s)", name, definedNotifiers(notifiers)))
		case slices.Contains(names[:i], name):
			errs = append(errs, fmt.Errorf("notify %q is listed twice", name))
		}
	}

	return names, errs
}

func hasKey(pool map[string]map[string]any, name string) bool {
	_, ok := pool[name]

	return ok
}

// definedNotifiers renders the names of the notifier definitions for an error
// message.
func definedNotifiers(pool map[string]map[string]any) string {
	if len(pool) == 0 {
		return "no notifiers are defined"
	}

	return "defined: " + strings.Join(slices.Sorted(maps.Keys(pool)), ", ")
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

// asRefs converts the retriever or provider list of an instance. Every element
// is either a table, or a string that stands for { ref = "<string>" }. The
// decoder yields []map[string]any for [[instance.<kind>]] and []any for an
// inline array, which may mix both forms.
func asRefs(kind string, value any) ([]map[string]any, []error) {
	var items []any

	switch v := value.(type) {
	case []map[string]any:
		for _, table := range v {
			items = append(items, table)
		}
	case []any:
		items = v
	default:
		return nil, []error{fmt.Errorf(`%q must be an array of names of [%s.<name>] definitions or tables, for example ["name", { ref = "name" }] or [[instance.%s]]`,
			kind, kind, kind)}
	}

	tables := make([]map[string]any, 0, len(items))

	var errs []error

	for i, item := range items {
		switch v := item.(type) {
		case map[string]any:
			tables = append(tables, v)
		case string:
			if v == "" {
				errs = append(errs, fmt.Errorf("%s #%d: the name of a definition must not be empty", kind, i+1))
				continue
			}

			tables = append(tables, map[string]any{"ref": v})
		default:
			errs = append(errs, fmt.Errorf("%s #%d must be the name of a [%s.<name>] definition or a table, not %T", kind, i+1, kind, item))
		}
	}

	return tables, errs
}

// asStrings converts an array of strings. The decoder yields []any for an
// array, and an empty one must stay an empty list, not become nil.
func asStrings(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}

	names := make([]string, len(items))

	for i, item := range items {
		if names[i], ok = item.(string); !ok {
			return nil, false
		}
	}

	return names, true
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
// such as the zone, is a different one. A provider declared inline (no Ref)
// is never flagged: it does not name a shared definition, so there is nothing
// to compare it against.
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
