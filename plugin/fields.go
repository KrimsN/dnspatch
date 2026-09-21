package plugin

import (
	"fmt"
	"reflect"
	"strings"
)

// configTag names the struct tag holding the parameter name, matching the
// keys used in the configuration file.
const configTag = "toml"

// fieldSpec describes one configurable field of a plugin configuration
// struct. The index is a path, since fields of embedded structs are promoted
// into the outer struct.
type fieldSpec struct {
	index      []int
	key        string
	required   bool
	defaultVal string
	hasDefault bool
}

// configFields collects the configurable fields of a struct type, promoting
// the fields of embedded structs. It panics on two fields claiming the same
// parameter name, which no configuration file could then address.
func configFields(t reflect.Type) []fieldSpec {
	specs := collectFields(t, nil)

	taken := make(map[string]string, len(specs))
	for _, spec := range specs {
		lowered := strings.ToLower(spec.key)
		if first, ok := taken[lowered]; ok {
			panic(fmt.Sprintf("plugin: fields %s and %s of %s both take parameter %q",
				first, fieldPath(t, spec.index), t, spec.key))
		}
		taken[lowered] = fieldPath(t, spec.index)
	}

	return specs
}

// collectFields walks a struct type, following embedded structs. The prefix
// is the index path of the embedded struct being walked.
func collectFields(t reflect.Type, prefix []int) []fieldSpec {
	specs := make([]fieldSpec, 0, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)

		key, _, _ := strings.Cut(field.Tag.Get(configTag), ",")
		if key == "-" {
			continue
		}

		index := append(append(make([]int, 0, len(prefix)+1), prefix...), i)

		// An embedded struct without a name of its own contributes its
		// fields to the outer struct, so that a plugin can share a base
		// configuration without nesting it in a table.
		if field.Anonymous && key == "" && field.Type.Kind() == reflect.Struct {
			specs = append(specs, collectFields(field.Type, index)...)
			continue
		}

		if !field.IsExported() {
			continue
		}

		if key == "" {
			key = strings.ToLower(field.Name)
		}

		defaultVal, hasDefault := field.Tag.Lookup("default")

		specs = append(specs, fieldSpec{
			index:      index,
			key:        key,
			required:   field.Tag.Get("required") == "true",
			defaultVal: defaultVal,
			hasDefault: hasDefault,
		})
	}

	return specs
}

// fieldPath names a field by its Go path, such as Base.Timeout, for messages
// addressed to the author of the plugin.
func fieldPath(t reflect.Type, index []int) string {
	names := make([]string, 0, len(index))
	for _, i := range index {
		field := t.Field(i)
		names = append(names, field.Name)
		t = field.Type
	}

	return strings.Join(names, ".")
}
