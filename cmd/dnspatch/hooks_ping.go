//go:build ping

package main

import "github.com/dnspatch/dnspatch/internal/hooks/ping"

func init() {
	hooks = ping.BuildHooks
}
