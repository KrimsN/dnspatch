//go:build !ping && !redis && !notify_all

package main

import (
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/internal/hooks/notify"
)

// The plain build must not carry the optional features: that is what keeps its
// binary and image small, and what makes a config using them fail loudly.
func TestPlainBuildHasNoOptionalFeatures(t *testing.T) {
	if options().Hooks != nil {
		t.Error("Hooks is set in a build without the ping tag")
	}

	_, err := notify.Default.Build("redis", map[string]any{"address": "redis://localhost:6379/0"})
	if err == nil || !strings.Contains(err.Error(), "no notify backends compiled in") {
		t.Errorf("Build(redis) error = %v, want it to say no backend is compiled in", err)
	}
}
