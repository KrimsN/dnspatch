package plugin

import (
	"fmt"
	"reflect"

	"github.com/KrimsN/dnspatch/internal/paramspec"
)

// Decode converts a plugin's raw parameters into its configuration struct.
//
// Parameter names come from the `toml` struct tag, falling back to the
// lower-cased field name; a tag of "-" excludes the field. Names match
// case-insensitively, and two parameters differing only in case are an error.
// A field tagged `required:"true"` must be present in params, and a field
// tagged `default:"..."` takes that value when params does not set it.
// Parameters that match no field are reported as errors.
//
// Values keep the types the TOML parser produced: integers arrive as int64
// and are converted to the field's integer type, durations are written as
// strings such as "10s", and types implementing encoding.TextUnmarshaler,
// among them netip.Addr, are parsed from strings.
//
// A field of type any is not supported, since there is no type to convert to.
// A field of type []any or map[string]any takes a value of exactly that type
// as it is, and rejects a value of any other type.
//
// A field whose type is a struct is a nested table, validated whether or not
// the table appears in params, so that a required field inside an omitted
// table is still reported. A field whose type is a pointer to a struct is an
// optional block: it stays nil when params omits it. An embedded struct
// without a tag contributes its own fields as if they were declared in the
// outer struct, the way encoding/json promotes them.
//
// Decode panics when the configuration struct itself is malformed, that is
// when two of its fields claim the same parameter name.
//
// Errors describe the offending parameter only. Callers add the surrounding
// context, such as the instance and plugin names.
func Decode[C any](params map[string]any) (C, error) {
	var cfg C

	target := reflect.ValueOf(&cfg).Elem()
	if target.Kind() != reflect.Struct {
		return cfg, fmt.Errorf("plugin configuration must be a struct, got %s", target.Type())
	}

	if err := decodeStruct(target, params, ""); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// decodeStruct fills a struct from params. The prefix qualifies parameter
// names in error messages for nested structs.
func decodeStruct(target reflect.Value, params map[string]any, prefix string) error {
	matches, err := matchParams(params, configFields(target.Type()), prefix)
	if err != nil {
		return err
	}

	for _, match := range matches {
		field := target.FieldByIndex(match.spec.Index)
		name := prefix + match.spec.Key

		// A default is a fallback: when the user gave a value it is never
		// parsed, so a broken tag cannot fail a configuration that overrides it.
		if match.spec.HasDefault && !match.given {
			if err := setFromString(field, match.spec.Default); err != nil {
				return fmt.Errorf("invalid default for parameter %q: %w", name, err)
			}
		}

		if !match.given {
			// A nested table still carries required fields and defaults of
			// its own, so it is decoded from nothing rather than skipped.
			if field.Kind() == reflect.Struct && !paramspec.ParsesText(field.Type()) {
				if err := decodeStruct(field, nil, name+"."); err != nil {
					return err
				}
			}

			continue
		}

		if err := assignValue(field, match.value, name); err != nil {
			return err
		}
	}

	return nil
}
