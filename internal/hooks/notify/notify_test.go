package notify

import (
	"context"
	"strings"
	"testing"
)

type fakePublisher struct{}

func (fakePublisher) Publish(context.Context, string, []byte) error { return nil }
func (fakePublisher) Close() error                                  { return nil }

func TestRegistryBuildUsesTheRegisteredFactory(t *testing.T) {
	registry := NewRegistry()
	var gotParams map[string]any
	registry.Register("fake", func(params map[string]any) (Publisher, error) {
		gotParams = params
		return fakePublisher{}, nil
	})

	pub, err := registry.Build("fake", map[string]any{"address": "x"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pub == nil {
		t.Fatal("Build returned a nil Publisher with no error")
	}
	if gotParams["address"] != "x" {
		t.Errorf("params passed to the factory = %v, want address=x", gotParams)
	}
}

func TestRegistryBuildRejectsAnUnknownType(t *testing.T) {
	registry := NewRegistry()
	registry.Register("redis", func(map[string]any) (Publisher, error) { return fakePublisher{}, nil })

	_, err := registry.Build("rabbitmq", nil)
	if err == nil {
		t.Fatal("Build succeeded, want an error")
	}
	if !strings.Contains(err.Error(), `"rabbitmq"`) || !strings.Contains(err.Error(), "redis") {
		t.Errorf("error = %v, want it to name the unknown type and what is registered", err)
	}
}

func TestTopicPrefixFallsBackToTheDefault(t *testing.T) {
	if got := TopicPrefix(nil); got != DefaultTopicPrefix {
		t.Errorf("TopicPrefix(nil) = %q, want %q", got, DefaultTopicPrefix)
	}
	if got := TopicPrefix(map[string]any{"topic_prefix": ""}); got != DefaultTopicPrefix {
		t.Errorf("TopicPrefix with an empty override = %q, want the default", got)
	}
}

func TestTopicPrefixUsesTheOverride(t *testing.T) {
	got := TopicPrefix(map[string]any{"topic_prefix": "custom."})
	if got != "custom." {
		t.Errorf("TopicPrefix = %q, want %q", got, "custom.")
	}
}
