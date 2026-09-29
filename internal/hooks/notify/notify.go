// Package notify publishes instance status changes to whatever message broker
// an operator configured with a [notify.<name>] definition. The brokers are notifier
// plugins (plugin.Notifier, registered with plugin.RegisterNotifier; Redis
// today, RabbitMQ or MQTT can be added later as their own package under
// plugins/, without touching this package or the runner). BuildHook selects one
// by the table's "type", and Hook is the runner.Hook that turns completed cycles
// into published Events.
//
// A notifier is compiled into a build only when plugins/all imports it, which
// is behind build tags (redis, notify_all); that keeps a broker's client library
// out of the binary of a build that does not ask for it.
package notify

import (
	"log/slog"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/runner"
	"github.com/KrimsN/dnspatch/plugin"
)

// BuildHook builds the runner.Hook behind one [notify.<name>] definition from the
// notifiers registered in plugin.Default. It has the signature app.NotifyBuilder
// wants.
func BuildHook(cfg config.Plugin, log *slog.Logger) (runner.Hook, error) {
	n, err := plugin.Default.BuildNotifier(cfg.Type, cfg.Params)
	if err != nil {
		return nil, err
	}

	return NewHook(n, log), nil
}
