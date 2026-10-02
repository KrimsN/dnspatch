// Package notify publishes instance status changes to whatever message broker
// an operator configured with a [notify.<name>] definition. The brokers are notifier
// plugins (plugin.Notifier, registered with plugin.RegisterNotifier; Redis,
// RabbitMQ and MQTT today, another can be added later as its own package under
// plugins/, without touching this package or the runner). BuildConnection selects one
// by the table's "type", and Hook is the runner.EventHook that turns the events
// of an instance into published payloads.
//
// A notifier is compiled into a build only when plugins/all imports it, which
// is behind build tags (redis, rabbitmq, mqtt, notify_all); that keeps a
// broker's client library out of the binary of a build that does not ask for
// it.
package notify

import (
	"log/slog"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
)

// Connection is the broker connection behind one [notify.<name>] definition.
// Every instance that uses the definition gets its own Hook from it, with the
// event types that instance asked for, and all of them publish through the one
// connection.
type Connection struct {
	pub plugin.Notifier
	log *slog.Logger
}

// BuildConnection connects the [notify.<name>] definition cfg with the
// notifiers registered in plugin.Default.
func BuildConnection(cfg config.Plugin, log *slog.Logger) (*Connection, error) {
	n, err := plugin.Default.BuildNotifier(cfg.Type, cfg.Params)
	if err != nil {
		return nil, err
	}

	return &Connection{pub: n, log: log}, nil
}

// Hook returns the hook that publishes the given event types of an instance.
func (c *Connection) Hook(events []config.Event) runner.Hook {
	return newHook(c.pub, c.log, events)
}

// Close releases the connection. The daemon calls it once, after every instance
// has stopped.
func (c *Connection) Close() error {
	return c.pub.Close()
}
