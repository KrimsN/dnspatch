package plugin

import (
	"reflect"

	"github.com/KrimsN/dnspatch/internal/paramspec"
)

// configFields lists the configurable fields of a struct type. It panics on
// two fields claiming the same parameter name, which no configuration file
// could then address, and on a tag option that is unknown or repeated.
func configFields(t reflect.Type) []paramspec.Field {
	fields, err := paramspec.Fields(t)
	if err != nil {
		panic("plugin: " + err.Error())
	}

	return fields
}
