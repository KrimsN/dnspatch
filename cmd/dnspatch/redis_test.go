//go:build redis || notify_all

package main

import (
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

func TestRedisBackendIsCompiledIn(t *testing.T) {
	n, err := plugin.Default.BuildNotifier("redis", map[string]any{"address": "redis://localhost:6379/0"})
	if err != nil {
		t.Fatalf("BuildNotifier(redis): %v", err)
	}
	_ = n.Close()
}
