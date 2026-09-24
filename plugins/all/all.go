// Package all registers every built-in plugin through blank imports, so that
// a single import makes them all available to a command.
//
// Library users should import the individual plugin packages they need
// instead, to avoid pulling in the dependencies of the others.
package all

import (
	// Built-in plugins register themselves in init.
	_ "github.com/KrimsN/dnspatch/plugins/beget"
	_ "github.com/KrimsN/dnspatch/plugins/dyndns2"
	_ "github.com/KrimsN/dnspatch/plugins/dyndns2/dynu"
	_ "github.com/KrimsN/dnspatch/plugins/dyndns2/nicru"
	_ "github.com/KrimsN/dnspatch/plugins/dyndns2/noip"
	_ "github.com/KrimsN/dnspatch/plugins/icanhazip"
	_ "github.com/KrimsN/dnspatch/plugins/identme"
	_ "github.com/KrimsN/dnspatch/plugins/ifconfigco"
	_ "github.com/KrimsN/dnspatch/plugins/ipify"
	_ "github.com/KrimsN/dnspatch/plugins/netif"
	_ "github.com/KrimsN/dnspatch/plugins/regru"
	_ "github.com/KrimsN/dnspatch/plugins/rfc2136"
	_ "github.com/KrimsN/dnspatch/plugins/selectel"
	_ "github.com/KrimsN/dnspatch/plugins/twoip"
	_ "github.com/KrimsN/dnspatch/plugins/yandexcloud"
)
