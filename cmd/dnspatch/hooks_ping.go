//go:build ping

package main

import "github.com/KrimsN/dnspatch/internal/hooks/ping"

func init() {
	hooks = ping.BuildHooks
}
