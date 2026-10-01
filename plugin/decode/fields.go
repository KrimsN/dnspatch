package decode

import (
	"reflect"

	"github.com/dnspatch/dnspatch/internal/paramspec"
)

// Fields lists the configurable fields of a struct type. It panics on
// two fields claiming the same parameter name, which no configuration file
// could then address, and on a tag option that is unknown or repeated.
func Fields(t reflect.Type) []paramspec.Field {
	fields, err := paramspec.Fields(t)
	if err != nil {
		panic("plugin: " + err.Error())
	}

	return fields
}
