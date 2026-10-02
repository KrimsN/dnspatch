package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

var errIntervalType = errors.New(`"interval" must be a string such as "30s" or "5m"`)

// pools are the named definitions of a document, by kind.
type pools struct {
	retrievers map[string]map[string]any
	providers  map[string]map[string]any
	notifiers  map[string]map[string]any
}

// shape is a document whose layout has been checked.
type shape struct {
	pools
	instances []map[string]any
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
	Notify []rawNotify
}

// checkShape verifies the top-level layout of the document: known keys only,
// pools of named tables, and an array of instance tables. Definitions are
// checked for a type.
func checkShape(doc map[string]any) (shape, []error) {
	var sh shape

	errs := unknownKeys(doc, "interval", "retriever", "provider", "instance", "notify")

	var poolErrs []error

	sh.retrievers, poolErrs = checkPool("retriever", doc["retriever"])
	errs = append(errs, poolErrs...)

	sh.providers, poolErrs = checkPool("provider", doc["provider"])
	errs = append(errs, poolErrs...)

	sh.notifiers, poolErrs = checkPool("notify", doc["notify"])
	errs = append(errs, poolErrs...)

	if value, ok := doc["instance"]; ok {
		if sh.instances, ok = asTables(value); !ok {
			errs = append(errs, errors.New(`"instance" must be an array of tables: [[instance]]`))
		}
	}

	return sh, errs
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

	var wrongType bool

	var name *string

	if name, wrongType = stringField(table, "name"); wrongType {
		errs = append(errs, errors.New(`"name" must be a string`))
	} else if name != nil {
		in.Name = *name
	}

	if in.Interval, wrongType = stringField(table, "interval"); wrongType {
		errs = append(errs, errIntervalType)
	}

	if in.PingURL, wrongType = stringField(table, "ping_url"); wrongType {
		errs = append(errs, errors.New(`"ping_url" must be a string`))
	}

	var listErrs []error

	if value, ok := table["retriever"]; ok {
		in.Retrievers, listErrs = asRefs("retriever", value)
		errs = append(errs, listErrs...)
	}

	if value, ok := table["provider"]; ok {
		in.Providers, listErrs = asRefs("provider", value)
		errs = append(errs, listErrs...)
	}

	if value, ok := table["notify"]; ok {
		in.Notify, listErrs = checkNotifyList(value)
		errs = append(errs, listErrs...)
	}

	return in, errs
}

// stringField reads an optional string key of table. A missing key gives nil;
// a key of another type gives nil and wrongType.
func stringField(table map[string]any, key string) (text *string, wrongType bool) {
	value, ok := table[key]
	if !ok {
		return nil, false
	}

	s, isString := value.(string)
	if !isString {
		return nil, true
	}

	return &s, false
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
