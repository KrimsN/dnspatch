package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/dnspatch/dnspatch/internal/paramspec"
	"github.com/dnspatch/dnspatch/plugin"
)

// schemaVersion is the version of the schema.json format. It changes when a
// reader written for the previous version would misread the file, not when
// plugins or parameters are added.
const schemaVersion = 1

// Build tags that switch on a whole kind of plugins, as cmd/genplugins writes
// them into the build constraints of plugins/all.
const (
	allProvidersTag  = "providers_all"
	allRetrieversTag = "retrievers_all"
	allNotifiersTag  = "notify_all"
)

// schema is the machine-readable counterpart of docs/PARAMETERS.md, for tools
// that build a configuration or a binary: it lists every built-in plugin, the
// build tags that bring it in and its parameters.
type schema struct {
	SchemaVersion int            `json:"schema_version"`
	Plugins       []schemaPlugin `json:"plugins"`
}

type schemaPlugin struct {
	Kind plugin.Kind `json:"kind"`
	// Name is the value of the type parameter in the configuration file.
	Name string `json:"name"`
	// BuildTags lists the tags of which any one compiles the plugin in.
	BuildTags []string      `json:"build_tags"`
	Fields    []schemaField `json:"fields"`
}

type schemaField struct {
	// Name is the key as written in the configuration file; a parameter of a
	// nested table is prefixed with the table name, as in "auth.token".
	Name string `json:"name"`
	// Type is one of string, boolean, integer, number, duration, array or table.
	Type string `json:"type"`
	// Required is set for a parameter that has to be given; when RequiredIf is
	// also set, only once that optional table is present.
	Required   bool   `json:"required"`
	RequiredIf string `json:"required_if,omitempty"`
	// Default is the text of the default value, absent when there is none.
	Default *string `json:"default,omitempty"`
	Example *string `json:"example,omitempty"`
	Doc     string  `json:"description"`
	// Secret marks a password, a key or the like.
	Secret bool `json:"secret"`
}

// renderSchema builds schema.json. Plugins come ordered by kind and name and
// fields in declaration order, so repeated runs give the same bytes.
func renderSchema(retrievers, providers, notifiers map[string]reflect.Type) ([]byte, error) {
	out := schema{SchemaVersion: schemaVersion, Plugins: []schemaPlugin{}}

	kinds := []struct {
		kind    plugin.Kind
		allTag  string
		plugins map[string]reflect.Type
	}{
		{plugin.KindRetriever, allRetrieversTag, retrievers},
		{plugin.KindProvider, allProvidersTag, providers},
		{plugin.KindNotifier, allNotifiersTag, notifiers},
	}

	for _, k := range kinds {
		for _, name := range slices.Sorted(maps.Keys(k.plugins)) {
			fields, err := schemaFields(k.plugins[name])
			if err != nil {
				return nil, fmt.Errorf("%s %q: %w", k.kind, name, err)
			}

			out.Plugins = append(out.Plugins, schemaPlugin{
				Kind:      k.kind,
				Name:      name,
				BuildTags: []string{name, k.allTag},
				Fields:    fields,
			})
		}
	}

	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(out); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// schemaFields describes the parameters of one plugin configuration, from the
// same parameter list as the reference document.
func schemaFields(t reflect.Type) ([]schemaField, error) {
	params, err := describe(t)
	if err != nil {
		return nil, err
	}

	secrets, err := secretNames(t, "")
	if err != nil {
		return nil, err
	}

	fields := make([]schemaField, 0, len(params))

	for _, p := range params {
		f := schemaField{
			Name:       p.name,
			Type:       schemaType(p.typ),
			Required:   p.required,
			RequiredIf: p.optionalTable,
			Doc:        p.doc,
			Secret:     secrets[p.name],
		}

		if p.hasDefault {
			f.Default = &p.defaultVal
		}

		if p.hasExample {
			f.Example = &p.example
		}

		fields = append(fields, f)
	}

	return fields, nil
}

// secretNames collects the dotted names of the parameters tagged as secret,
// which describe does not carry.
func secretNames(t reflect.Type, prefix string) (map[string]bool, error) {
	fields, err := paramspec.Fields(t)
	if err != nil {
		return nil, err
	}

	secrets := make(map[string]bool)

	for _, field := range fields {
		name := prefix + field.Key

		ft := field.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}

		if ft.Kind() == reflect.Struct && !paramspec.ParsesText(ft) {
			nested, err := secretNames(ft, name+".")
			if err != nil {
				return nil, err
			}

			maps.Copy(secrets, nested)

			continue
		}

		if field.Secret {
			secrets[name] = true
		}
	}

	return secrets, nil
}

// schemaType names the kind of value a parameter takes. A type that parses
// itself from text, such as netip.Addr, is written as a string.
func schemaType(t reflect.Type) string {
	switch {
	case t == durationType:
		return "duration"
	case paramspec.ParsesText(t):
		return "string"
	}

	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "table"
	default:
		return "string"
	}
}
