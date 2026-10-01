//go:build !ping && !redis && !rabbitmq && !notify_all && !dnspatch_none

package main

import (
	"strings"
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

// The plain build must not carry the notifier backends: that is what keeps its
// binary and image small, and what makes a config using them fail loudly.
func TestPlainBuildHasNoNotifiers(t *testing.T) {
	_, err := plugin.Default.BuildNotifier("redis", map[string]any{"address": "redis://localhost:6379/0"})
	if err == nil || !strings.Contains(err.Error(), "not compiled into this build") {
		t.Errorf("BuildNotifier(redis) error = %v, want it to say the backend is not compiled in", err)
	}
}

// The plain build has every retriever and provider, which is what the
// dnspatch image ships.
func TestPlainBuildHasEveryRetrieverAndProvider(t *testing.T) {
	for _, k := range plugin.Default.Known() {
		if k.Kind == plugin.KindNotifier {
			continue
		}

		if !plugin.Default.Registered(k.Kind, k.Name) {
			t.Errorf("%s %q is missing from the plain build", k.Kind, k.Name)
		}
	}
}
