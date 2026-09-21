package main

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"
)

// configTag names the struct tag holding the parameter name. It must stay in
// step with the tag plugin.Decode reads.
const configTag = "toml"

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// param is one documented parameter of a plugin configuration.
type param struct {
	// name is the key as written in the configuration file; a parameter of a
	// nested table is prefixed with the table name, as in "auth.token".
	name       string
	required   bool
	defaultVal string
	hasDefault bool
	doc        string
	// example is the value the `example` tag suggests, used by the generated
	// configuration template.
	example    string
	hasExample bool
	// typ is the type of the parameter with any pointer stripped.
	typ reflect.Type
	// optionalTable is the name of the optional table the parameter lives in,
	// or empty. A required parameter of such a table is only required once
	// the table is present.
	optionalTable string
}

// describe lists the parameters of a plugin configuration struct in
// declaration order. It walks the struct the way plugin.Decode does: fields of
// embedded structs are promoted, `toml:"-"` and unexported fields are skipped,
// a struct field is a nested table, and a pointer to a struct is an optional
// one.
func describe(t reflect.Type) ([]param, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("configuration type %s is not a struct", t)
	}

	params := collect(t, "", "")

	taken := make(map[string]struct{}, len(params))
	for _, p := range params {
		lowered := strings.ToLower(p.name)
		if _, ok := taken[lowered]; ok {
			return nil, fmt.Errorf("configuration type %s: parameter %q is declared twice", t, p.name)
		}
		taken[lowered] = struct{}{}
	}

	return params, nil
}

// collect appends the parameters of a struct type. The prefix qualifies names
// inside a nested table, and optionalTable is inherited from the pointer that
// introduced it.
func collect(t reflect.Type, prefix, optionalTable string) []param {
	var params []param

	for i := range t.NumField() {
		field := t.Field(i)

		key, _, _ := strings.Cut(field.Tag.Get(configTag), ",")
		if key == "-" {
			continue
		}

		if field.Anonymous && key == "" && field.Type.Kind() == reflect.Struct {
			params = append(params, collect(field.Type, prefix, optionalTable)...)
			continue
		}

		if !field.IsExported() {
			continue
		}

		if key == "" {
			key = strings.ToLower(field.Name)
		}

		name := prefix + key

		fieldType, optional := field.Type, false
		if fieldType.Kind() == reflect.Pointer {
			fieldType, optional = fieldType.Elem(), true
		}

		if fieldType.Kind() == reflect.Struct && !isLeaf(fieldType) {
			table := optionalTable
			if optional && table == "" {
				table = name
			}
			params = append(params, collect(fieldType, name+".", table)...)
			continue
		}

		defaultVal, hasDefault := field.Tag.Lookup("default")
		example, hasExample := field.Tag.Lookup("example")

		params = append(params, param{
			name:          name,
			required:      field.Tag.Get("required") == "true",
			defaultVal:    defaultVal,
			hasDefault:    hasDefault,
			doc:           field.Tag.Get("doc"),
			example:       example,
			hasExample:    hasExample,
			typ:           fieldType,
			optionalTable: optionalTable,
		})
	}

	return params
}

// isLeaf reports whether a struct is written as a single value rather than as
// a table of its own, as time.Time and netip.Addr are.
func isLeaf(t reflect.Type) bool {
	return reflect.PointerTo(t).Implements(textUnmarshalerType)
}
