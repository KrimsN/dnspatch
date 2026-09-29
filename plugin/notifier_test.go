package plugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

type fakeNotifierConfig struct {
	Address string `toml:"address,required"`

	plugin.NotifierCommon
}

// topicSink remembers what it was asked to publish.
type topicSink struct {
	address string
	topics  []string
}

func (s *topicSink) Publish(_ context.Context, topic string, _ []byte) error {
	s.topics = append(s.topics, topic)
	return nil
}

func (s *topicSink) Close() error { return nil }

func newNotifierRegistry(sink *topicSink) *plugin.Registry {
	registry := plugin.NewRegistry()
	plugin.RegisterNotifierIn(registry, "fake", func(cfg fakeNotifierConfig) (plugin.Notifier, error) {
		sink.address = cfg.Address
		return sink, nil
	})

	return registry
}

func TestBuildNotifierDecodesTheConfigurationAndPrefixesTheTopic(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"default prefix": {map[string]any{"address": "x"}, "dnspatch.events.home"},
		"custom prefix":  {map[string]any{"address": "x", "topic_prefix": "custom."}, "custom.home"},
	} {
		t.Run(name, func(t *testing.T) {
			sink := &topicSink{}

			n, err := newNotifierRegistry(sink).BuildNotifier("fake", tc.params)
			if err != nil {
				t.Fatalf("BuildNotifier: %v", err)
			}

			if sink.address != "x" {
				t.Errorf("address = %q, want %q", sink.address, "x")
			}

			if err := n.Publish(context.Background(), "home", nil); err != nil {
				t.Fatalf("Publish: %v", err)
			}

			if len(sink.topics) != 1 || sink.topics[0] != tc.want {
				t.Errorf("published topics = %v, want [%s]", sink.topics, tc.want)
			}
		})
	}
}

func TestBuildNotifierReportsBadParameters(t *testing.T) {
	registry := newNotifierRegistry(&topicSink{})

	_, err := registry.BuildNotifier("fake", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("error without the required parameter = %v, want it to name address", err)
	}

	_, err = registry.BuildNotifier("fake", map[string]any{"address": "x", "topic_prefx": "a."})
	if err == nil || !strings.Contains(err.Error(), "topic_prefix") {
		t.Errorf("error with a misspelled parameter = %v, want a hint at topic_prefix", err)
	}
}

func TestNotifierConfigTypesIncludeTheCommonParameters(t *testing.T) {
	types := newNotifierRegistry(&topicSink{}).NotifierConfigTypes()

	if len(types) != 1 || types["fake"] == nil {
		t.Fatalf("NotifierConfigTypes() = %v, want one entry, fake", types)
	}

	if _, ok := types["fake"].FieldByName("NotifierCommon"); !ok {
		t.Error("the configuration type of the notifier lacks the embedded NotifierCommon")
	}
}

func TestUnknownNotifierTypeIsReportedLikeTheOtherKinds(t *testing.T) {
	_, err := newNotifierRegistry(&topicSink{}).BuildNotifier("rabbitmq", nil)
	if err == nil || !strings.Contains(err.Error(), `unknown notifier type "rabbitmq" (registered: fake)`) {
		t.Errorf("error = %v", err)
	}
}

func TestDeclaredPluginsThatAreNotRegisteredSayHowToGetThem(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterProviderIn(registry, "fake", newProvider)
	registry.Declare(plugin.KindProvider, "fake", "unused")
	registry.Declare(plugin.KindProvider, "absent", `rebuild with the "absent" tag`)
	registry.Declare(plugin.KindRetriever, "fake", "for retrievers")

	_, err := registry.BuildProvider("absent", nil)
	if err == nil || !strings.Contains(err.Error(), `provider type "absent" is not compiled into this build: rebuild with the "absent" tag`) {
		t.Errorf("BuildProvider(absent) error = %v", err)
	}

	// The hint belongs to its own kind: a retriever of the same name is another plugin.
	_, err = registry.BuildRetriever("fake", nil)
	if err == nil || !strings.Contains(err.Error(), "for retrievers") {
		t.Errorf("BuildRetriever(fake) error = %v, want the hint of the retriever", err)
	}

	if !registry.Registered(plugin.KindProvider, "fake") || registry.Registered(plugin.KindProvider, "absent") || registry.Registered(plugin.KindNotifier, "fake") {
		t.Error("Registered does not tell what is compiled in")
	}

	if got := len(registry.Known()); got != 3 {
		t.Errorf("Known() has %d entries, want 3", got)
	}
}
