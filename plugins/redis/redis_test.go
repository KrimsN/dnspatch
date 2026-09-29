package redis

import (
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

func build(t *testing.T, params map[string]any) (plugin.Notifier, error) {
	t.Helper()

	return plugin.Default.BuildNotifier(Name, params)
}

func TestNotifierRequiresAnAddress(t *testing.T) {
	_, err := build(t, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("BuildNotifier(no address) error = %v, want it to name the missing address", err)
	}
}

func TestNotifierRejectsAMalformedAddress(t *testing.T) {
	if _, err := build(t, map[string]any{"address": "not-a-url"}); err == nil {
		t.Fatal("BuildNotifier succeeded on a malformed address, want an error")
	}
}

func TestNotifierBuildsFromAValidAddress(t *testing.T) {
	n, err := build(t, map[string]any{"address": "redis://localhost:6379/0"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	// go-redis dials lazily: building the client must not itself connect, so
	// this must succeed even with nothing listening on localhost:6379.
	if err := n.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNotifierTakesATopicPrefix(t *testing.T) {
	n, err := build(t, map[string]any{"address": "redis://localhost:6379/0", "topic_prefix": "custom."})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	_ = n.Close()
}
