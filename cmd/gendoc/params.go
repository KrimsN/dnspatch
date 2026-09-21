package main

import (
	"fmt"
	"reflect"

	"github.com/KrimsN/dnspatch/internal/paramspec"
)

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
// declaration order. The fields come from paramspec, as they do for
// plugin.Decode; on top of that a struct field is a nested table, whose
// parameters are listed under its name, and a pointer to a struct is an
// optional one.
func describe(t reflect.Type) ([]param, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("configuration type %s is not a struct", t)
	}

	params, err := collect(t, "", "")
	if err != nil {
		return nil, fmt.Errorf("configuration type %s: %w", t, err)
	}

	return params, nil
}

// collect appends the parameters of a struct type. The prefix qualifies names
// inside a nested table, and optionalTable is inherited from the pointer that
// introduced it.
func collect(t reflect.Type, prefix, optionalTable string) ([]param, error) {
	fields, err := paramspec.Fields(t)
	if err != nil {
		return nil, err
	}

	var params []param

	for _, field := range fields {
		name := prefix + field.Key

		fieldType, optional := field.Type, false
		if fieldType.Kind() == reflect.Pointer {
			fieldType, optional = fieldType.Elem(), true
		}

		if fieldType.Kind() == reflect.Struct && !paramspec.ParsesText(fieldType) {
			table := optionalTable
			if optional && table == "" {
				table = name
			}

			nested, err := collect(fieldType, name+".", table)
			if err != nil {
				return nil, err
			}
			params = append(params, nested...)

			continue
		}

		params = append(params, param{
			name:          name,
			required:      field.Required,
			defaultVal:    field.Default,
			hasDefault:    field.HasDefault,
			doc:           field.Doc,
			example:       field.Example,
			hasExample:    field.HasExample,
			typ:           fieldType,
			optionalTable: optionalTable,
		})
	}

	return params, nil
}
