package redis

import (
	"strings"
	"testing"
)

func TestFactoryRequiresAnAddress(t *testing.T) {
	_, err := Factory(map[string]any{})
	if err == nil || !strings.Contains(err.Error(), `"address" is required`) {
		t.Errorf("Factory(no address) error = %v, want it to name the missing address", err)
	}
}

func TestFactoryRejectsAMalformedAddress(t *testing.T) {
	_, err := Factory(map[string]any{"address": "not-a-url"})
	if err == nil {
		t.Fatal("Factory succeeded on a malformed address, want an error")
	}
}

func TestFactoryBuildsAPublisherFromAValidAddress(t *testing.T) {
	pub, err := Factory(map[string]any{"address": "redis://localhost:6379/0"})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if pub == nil {
		t.Fatal("Factory returned a nil Publisher with no error")
	}
	// go-redis dials lazily: building the client must not itself connect, so
	// this must succeed even with nothing listening on localhost:6379.
	if err := pub.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
