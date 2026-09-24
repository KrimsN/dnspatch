package main

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/paramspec"
	"github.com/KrimsN/dnspatch/internal/runner"
	"github.com/KrimsN/dnspatch/plugin"
)

// maskedValue stands in for the value of a secret parameter.
const maskedValue = "***"

// elidedValue stands in for a value that is not a plain scalar, such as a
// table: any secret inside it would be beyond the reach of the top-level
// secret flag.
const elidedValue = "..."

// printConfigSummary confirms that path was read and parsed as intended: one
// line per instance naming its interval and the retrievers and providers it
// resolved to, so the user can tell the parsed config apart from a typo that
// silently fell back to a default (an empty ref, a misspelled family, ...).
// Instance i of instances was built from cfg.Instances[i].
func printConfigSummary(stdout io.Writer, path string, cfg config.Config, instances []runner.Instance, registry *plugin.Registry) {
	_, _ = fmt.Fprintf(stdout, "dnspatch: config OK: %s (%d instance(s))\n", path, len(instances))

	params := providerParams(registry)

	for i, in := range instances {
		_, _ = fmt.Fprintf(stdout, "  %s: interval=%s retrievers=%s providers=%s\n",
			in.Name, in.Interval, describeRetrievers(in.Retrievers), describeProviders(cfg.Instances[i].Providers, params))
	}
}

// describeRetrievers renders an instance's retrievers as "name(family)",
// omitting the family when it was not set.
func describeRetrievers(retrievers []runner.NamedRetriever) string {
	names := make([]string, len(retrievers))
	for i, r := range retrievers {
		if r.Family == "" {
			names[i] = r.Name
		} else {
			names[i] = fmt.Sprintf("%s(%s)", r.Name, r.Family)
		}
	}

	return "[" + strings.Join(names, ", ") + "]"
}

// describeProviders renders an instance's providers by name. Providers of one
// type are told apart, the way a retriever's family is shown, by the
// parameters on which they differ: "regru(zone=example.org, rr_name=office)".
// A provider that is the only one of its type is shown by name alone.
func describeProviders(providers []config.Plugin, params map[string][]paramspec.Field) string {
	labels := make([]string, len(providers))
	byType := make(map[string][]int)

	for i, p := range providers {
		labels[i] = p.Name()
		byType[p.Type] = append(byType[p.Type], i)
	}

	for typ, indexes := range byType {
		if len(indexes) < 2 {
			continue
		}

		group := make([]config.Plugin, len(indexes))
		for j, i := range indexes {
			group[j] = providers[i]
		}

		for j, detail := range differences(group, params[typ]) {
			if detail != "" {
				labels[indexes[j]] += "(" + detail + ")"
			}
		}
	}

	return "[" + strings.Join(labels, ", ") + "]"
}

// differences renders, for each plugin of a group of one type, the parameters
// that are not the same in the whole group, in the order the plugin declares
// them, as "key=value". A parameter the plugin leaves out is not listed for it.
// Only declared parameters are listed, so a name the type does not know is
// never printed.
//
// A secret parameter is listed, its value masked, only when secrets are all
// that tell the plugins apart. Next to a difference that can be shown it would
// only be noise.
func differences(group []config.Plugin, fields []paramspec.Field) []string {
	given := make([]map[string]any, len(group))
	for i, p := range group {
		// The decoder matches parameter names ignoring case, so must this.
		given[i] = make(map[string]any, len(p.Params))
		for key, value := range p.Params {
			given[i][strings.ToLower(key)] = value
		}
	}

	var differing []paramspec.Field

	for _, field := range fields {
		if differ(given, strings.ToLower(field.Key)) {
			differing = append(differing, field)
		}
	}

	if slices.ContainsFunc(differing, func(f paramspec.Field) bool { return !f.Secret }) {
		differing = slices.DeleteFunc(differing, func(f paramspec.Field) bool { return f.Secret })
	}

	details := make([][]string, len(group))

	for _, field := range differing {
		key := strings.ToLower(field.Key)

		for i := range group {
			value, ok := given[i][key]
			if !ok {
				continue
			}

			details[i] = append(details[i], field.Key+"="+displayValue(value, field.Secret))
		}
	}

	rendered := make([]string, len(group))
	for i, d := range details {
		rendered[i] = strings.Join(d, ", ")
	}

	return rendered
}

// differ reports whether the parameter is set to different values, or set in
// some plugins and not in others.
func differ(given []map[string]any, key string) bool {
	first, firstSet := given[0][key]

	for _, other := range given[1:] {
		value, set := other[key]
		if set != firstSet || !reflect.DeepEqual(value, first) {
			return true
		}
	}

	return false
}

// displayValue renders a parameter value for the summary: a secret is masked,
// and only a plain scalar is printed as it is.
func displayValue(value any, secret bool) string {
	if secret {
		return maskedValue
	}

	switch value.(type) {
	case string, bool, int64, float64:
		return fmt.Sprint(value)
	default:
		return elidedValue
	}
}

// providerParams lists the parameters of every registered provider type, keyed
// by type name, in declaration order.
func providerParams(registry *plugin.Registry) map[string][]paramspec.Field {
	types := registry.ProviderConfigTypes()
	params := make(map[string][]paramspec.Field, len(types))

	for name, t := range types {
		fields, err := paramspec.Fields(t)
		if err != nil {
			// Registration rejects a malformed configuration struct, so a
			// registered type cannot get here.
			panic("dnspatch: " + err.Error())
		}

		params[name] = fields
	}

	return params
}
