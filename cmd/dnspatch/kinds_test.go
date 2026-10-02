//go:build dnspatch_none && retrievers_all && cloudflare && !providers_all && !notify_all && !redis && !rabbitmq && !mqtt

package main

import (
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

// retrievers_all brings back every retriever and nothing else, so it can be
// combined with the tag of a single provider.
func TestAKindTagBringsBackOnlyItsKind(t *testing.T) {
	for _, k := range plugin.Default.Known() {
		want := k.Kind == plugin.KindRetriever || (k.Kind == plugin.KindProvider && k.Name == "cloudflare")

		if got := plugin.Default.Registered(k.Kind, k.Name); got != want {
			t.Errorf("%s %q registered = %v, want %v", k.Kind, k.Name, got, want)
		}
	}
}
