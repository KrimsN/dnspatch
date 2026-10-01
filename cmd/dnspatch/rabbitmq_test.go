//go:build rabbitmq || notify_all

package main

import (
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

func TestRabbitMQBackendIsCompiledIn(t *testing.T) {
	n, err := plugin.Default.BuildNotifier("rabbitmq", map[string]any{"address": "amqp://localhost:5672/"})
	if err != nil {
		t.Fatalf("BuildNotifier(rabbitmq): %v", err)
	}
	_ = n.Close()
}
