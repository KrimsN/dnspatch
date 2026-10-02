//go:build dnspatch_none && cloudflare && ipify

package main

import (
	"strings"
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

// With dnspatch_none, only the plugins whose own tag is set are compiled in,
// and asking for another says which tag brings it. Notifiers are not looked at:
// they have tags of their own, and a build may or may not carry any.
func TestOnlyTheChosenPluginsAreCompiledIn(t *testing.T) {
	for _, k := range plugin.Default.Known() {
		if k.Kind == plugin.KindNotifier {
			continue
		}

		want := (k.Kind == plugin.KindProvider && k.Name == "cloudflare") || (k.Kind == plugin.KindRetriever && k.Name == "ipify")

		if got := plugin.Default.Registered(k.Kind, k.Name); got != want {
			t.Errorf("%s %q registered = %v, want %v", k.Kind, k.Name, got, want)
		}
	}

	_, err := plugin.Default.BuildProvider("beget", nil)
	if err == nil || !strings.Contains(err.Error(), `"beget" build tag`) {
		t.Errorf("BuildProvider(beget) error = %v, want it to name the beget tag", err)
	}
}
