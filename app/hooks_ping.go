//go:build ping

package app

import "github.com/dnspatch/dnspatch/internal/hooks/ping"

func init() {
	hooks = ping.BuildHooks
}
