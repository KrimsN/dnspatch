//go:build !ping && !dnspatch_none

package main

import (
	"strings"
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

// A notifier backend that a build does not carry must fail loudly and say so,
// which is what keeps the plain build small and a config that uses a backend
// from being silently ignored. It holds for any build, whichever notifier tags
// it was made with, so it needs no list of them.
func TestANotifierMissingFromTheBuildSaysSo(t *testing.T) {
	for _, k := range plugin.Default.Known() {
		if k.Kind != plugin.KindNotifier || plugin.Default.Registered(k.Kind, k.Name) {
			continue
		}

		_, err := plugin.Default.BuildNotifier(k.Name, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "not compiled into this build") {
			t.Errorf("BuildNotifier(%s) error = %v, want it to say the backend is not compiled in", k.Name, err)
		}
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
