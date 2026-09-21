// Package all registers every built-in plugin through blank imports, so that
// a single import makes them all available to a command.
//
// Library users should import the individual plugin packages they need
// instead, to avoid pulling in the dependencies of the others.
package all

import (
	// Built-in plugins register themselves in init.
	_ "github.com/KrimsN/dnspatch/plugins/ifconfigco"
	_ "github.com/KrimsN/dnspatch/plugins/regru"
)
