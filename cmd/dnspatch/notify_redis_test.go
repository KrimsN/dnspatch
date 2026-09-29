//go:build redis || notify_all

package main

import (
	"testing"

	"github.com/KrimsN/dnspatch/internal/hooks/notify"
)

func TestRedisBackendIsCompiledIn(t *testing.T) {
	pub, err := notify.Default.Build("redis", map[string]any{"address": "redis://localhost:6379/0"})
	if err != nil {
		t.Fatalf("Build(redis): %v", err)
	}
	_ = pub.Close()
}
