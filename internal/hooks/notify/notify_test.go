package notify

import (
	"context"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/runner"
	"github.com/KrimsN/dnspatch/plugin"
)

// topicRecorder is the notifier the tests register; it remembers the topics it
// was asked to publish under.
type topicRecorder struct{ topics *[]string }

func (r topicRecorder) Publish(_ context.Context, topic string, _ []byte) error {
	*r.topics = append(*r.topics, topic)
	return nil
}

func (topicRecorder) Close() error { return nil }

type fakeConfig struct {
	plugin.NotifierCommon
}

var published []string

func init() {
	plugin.RegisterNotifier("fake", func(fakeConfig) (plugin.Notifier, error) {
		return topicRecorder{topics: &published}, nil
	})
}

func TestBuildHookPublishesUnderTheTopicPrefix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"default prefix", nil, "dnspatch.events.home"},
		{"custom prefix", map[string]any{"topic_prefix": "custom."}, "custom.home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			published = nil

			hook, err := BuildHook(config.Plugin{Type: "fake", Params: tc.params}, discardLogger())
			if err != nil {
				t.Fatalf("BuildHook: %v", err)
			}

			hook.AfterCycle(context.Background(), runner.CycleEvent{Instance: "home", Success: false})

			if len(published) != 1 || published[0] != tc.want {
				t.Errorf("published topics = %v, want [%s]", published, tc.want)
			}
		})
	}
}

func TestBuildHookRejectsAnUnknownType(t *testing.T) {
	_, err := BuildHook(config.Plugin{Type: "rabbitmq"}, discardLogger())
	if err == nil {
		t.Fatal("BuildHook succeeded, want an error")
	}
	if !strings.Contains(err.Error(), `"rabbitmq"`) || !strings.Contains(err.Error(), "fake") {
		t.Errorf("error = %v, want it to name the unknown type and what is registered", err)
	}
}
