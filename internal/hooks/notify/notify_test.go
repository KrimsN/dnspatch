package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
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

func TestBuildConnectionPublishesUnderTheTopicPrefix(t *testing.T) {
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

			conn, err := BuildConnection(config.Plugin{Type: "fake", Params: tc.params}, discardLogger())
			if err != nil {
				t.Fatalf("BuildConnection: %v", err)
			}

			hook := conn.Hook([]config.Event{config.EventCycle}).(runner.EventHook)
			hook.OnEvent(context.Background(), runner.Event{Kind: runner.KindCycle, Instance: "home"})

			if len(published) != 1 || published[0] != tc.want {
				t.Errorf("published topics = %v, want [%s]", published, tc.want)
			}
		})
	}
}

func TestBuildConnectionRejectsAnUnknownType(t *testing.T) {
	_, err := BuildConnection(config.Plugin{Type: "rabbitmq"}, discardLogger())
	if err == nil {
		t.Fatal("BuildHook succeeded, want an error")
	}
	if !strings.Contains(err.Error(), `"rabbitmq"`) || !strings.Contains(err.Error(), "fake") {
		t.Errorf("error = %v, want it to name the unknown type and what is registered", err)
	}
}

type closeRecorder struct {
	topicRecorder
	closed int
}

func (c *closeRecorder) Close() error {
	c.closed++

	return errors.New("close failed")
}

func TestConnectionCloseClosesTheNotifier(t *testing.T) {
	pub := &closeRecorder{}
	conn := &Connection{pub: pub, log: discardLogger()}

	if err := conn.Close(); err == nil || err.Error() != "close failed" {
		t.Errorf("Close error = %v, want the notifier's error passed through", err)
	}

	if pub.closed != 1 {
		t.Errorf("the notifier was closed %d times, want once", pub.closed)
	}
}

func TestAfterCycleDoesNotPublish(t *testing.T) {
	pub := &recordingPublisher{}
	hook := newHook(pub, discardLogger(), []config.Event{config.EventCycle, config.EventStatus})

	hook.AfterCycle(context.Background(), runner.CycleEvent{})

	if got := pub.all(); len(got) != 0 {
		t.Errorf("AfterCycle published %v, want nothing: events come through OnEvent", got)
	}
}
