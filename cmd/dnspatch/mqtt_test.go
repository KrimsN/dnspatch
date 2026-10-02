//go:build mqtt || notify_all

package main

import (
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

func TestMQTTBackendIsCompiledIn(t *testing.T) {
	n, err := plugin.Default.BuildNotifier("mqtt", map[string]any{"address": "mqtt://localhost:1883"})
	if err != nil {
		t.Fatalf("BuildNotifier(mqtt): %v", err)
	}
	_ = n.Close()
}
