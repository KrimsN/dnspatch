// Package paramspec reads the parameters of a plugin configuration struct
// from its field tags. plugin.Decode fills the struct from it and cmd/gendoc
// documents it, so the two cannot disagree about which parameters exist.
package paramspec

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"
)

// tag names the struct tag holding the parameter name, matching the keys used
// in the configuration file.
const tag = "toml"

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// Field describes one parameter of a plugin configuration struct.
type Field struct {
	// Index is the path to the field, since the fields of embedded structs
	// are promoted into the outer struct. Use it with reflect.Value.FieldByIndex.
	Index []int
	// Key is the parameter name as written in the configuration file.
	Key string
	// Type is the declared type of the field.
	Type reflect.Type

	Required   bool
	Default    string
	HasDefault bool
	Doc        string
	Example    string
	HasExample bool
}

// Fields lists the parameters of a struct type in declaration order. Fields of
// embedded structs without a name of their own are promoted, the way
// encoding/json does; fields tagged `toml:"-"` and unexported fields are
// skipped. A field that is itself a table, a struct or a pointer to one, is
// listed as one parameter; walk into it with Fields on its type, unless
// ParsesText says it is written as a single value.
//
// Two fields claiming the same name, ignoring case, are an error: no
// configuration file could address them separately.
func Fields(t reflect.Type) ([]Field, error) {
	fields := collect(t, nil)

	taken := make(map[string]string, len(fields))
	for _, f := range fields {
		lowered := strings.ToLower(f.Key)
		if first, ok := taken[lowered]; ok {
			return nil, fmt.Errorf("fields %s and %s of %s both take parameter %q",
				first, goPath(t, f.Index), t, f.Key)
		}
		taken[lowered] = goPath(t, f.Index)
	}

	return fields, nil
}

// ParsesText reports whether a type parses itself from text, as netip.Addr
// does. A struct of such a type is written as a single value rather than as a
// table of its own.
func ParsesText(t reflect.Type) bool {
	return t.Implements(textUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType)
}

// collect walks a struct type, following embedded structs. The prefix is the
// index path of the embedded struct being walked.
func collect(t reflect.Type, prefix []int) []Field {
	fields := make([]Field, 0, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)

		key, _, _ := strings.Cut(field.Tag.Get(tag), ",")
		if key == "-" {
			continue
		}

		index := append(append(make([]int, 0, len(prefix)+1), prefix...), i)

		// An embedded struct without a name of its own contributes its
		// fields to the outer struct, so that a plugin can share a base
		// configuration without nesting it in a table.
		if field.Anonymous && key == "" && field.Type.Kind() == reflect.Struct {
			fields = append(fields, collect(field.Type, index)...)
			continue
		}

		if !field.IsExported() {
			continue
		}

		if key == "" {
			key = strings.ToLower(field.Name)
		}

		defaultVal, hasDefault := field.Tag.Lookup("default")
		example, hasExample := field.Tag.Lookup("example")

		fields = append(fields, Field{
			Index:      index,
			Key:        key,
			Type:       field.Type,
			Required:   field.Tag.Get("required") == "true",
			Default:    defaultVal,
			HasDefault: hasDefault,
			Doc:        field.Tag.Get("doc"),
			Example:    example,
			HasExample: hasExample,
		})
	}

	return fields
}

// goPath names a field by its Go path, such as Base.Timeout, for messages
// addressed to the author of the plugin.
func goPath(t reflect.Type, index []int) string {
	names := make([]string, 0, len(index))
	for _, i := range index {
		field := t.Field(i)
		names = append(names, field.Name)
		t = field.Type
	}

	return strings.Join(names, ".")
}
