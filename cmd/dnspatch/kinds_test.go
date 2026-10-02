//go:build dnspatch_none && retrievers_all && cloudflare && !providers_all

package main

import (
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

// retrievers_all brings back every retriever and nothing else, so it can be
// combined with the tag of a single provider. Notifiers are not looked at: they
// have tags of their own, and a build may or may not carry any.
func TestAKindTagBringsBackOnlyItsKind(t *testing.T) {
	for _, k := range plugin.Default.Known() {
		if k.Kind == plugin.KindNotifier {
			continue
		}

		want := k.Kind == plugin.KindRetriever || (k.Kind == plugin.KindProvider && k.Name == "cloudflare")

		if got := plugin.Default.Registered(k.Kind, k.Name); got != want {
			t.Errorf("%s %q registered = %v, want %v", k.Kind, k.Name, got, want)
		}
	}
}
