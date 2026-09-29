// Package notify is the adapter layer between the runner and whatever
// message broker an operator wants instance status changes published to.
// Publisher is the seam a backend implements (Redis today; RabbitMQ or MQTT
// can be added later as their own subpackage, the way internal/hooks/notify/redis
// does it, without touching this package or the runner). Registry selects a
// backend by the [[notify]] table's "type", and Hook is the runner.Hook that
// turns completed cycles into published Events.
//
// A backend package registers itself in Default from its init function, so it
// is compiled into a build only when something imports it: cmd/dnspatch does
// that in files guarded by build tags (redis, notify_all, ...), which keeps a
// backend's client library out of the binary of a build that does not ask for it.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/runner"
)

// Default is the Registry the backend packages register in, and the one
// BuildHook reads.
var Default = NewRegistry()

// DefaultTopicPrefix is prepended to the instance name to form the topic (or
// channel, or routing key — the term a backend itself uses) an Event is
// published under, unless the [[notify]] table's own "topic_prefix" overrides it.
const DefaultTopicPrefix = "dnspatch.events."

// Publisher delivers one already-serialized event to a broker-specific
// destination (a channel, topic, routing key, ...). Close releases any
// connection the backend holds; the daemon does not otherwise shut hooks
// down; it will Close.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Close() error
}

// Factory builds a Publisher from the [[notify]] table's own parameters
// (everything except "type", which selected the Factory in the first place).
type Factory func(params map[string]any) (Publisher, error)

// Registry maps a [[notify]] "type" to the Factory that builds it. The zero
// value has no backends registered.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns an empty Registry, ready for Register calls.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a backend under typ, the value its [[notify]] table's "type"
// must have to select it. Registering the same typ twice replaces the
// earlier Factory.
func (r *Registry) Register(typ string, f Factory) {
	r.factories[typ] = f
}

// Build looks up typ and builds a Publisher from params. An unregistered typ
// is an error naming every type that is available, so a typo does not go
// unnoticed.
func (r *Registry) Build(typ string, params map[string]any) (Publisher, error) {
	f, ok := r.factories[typ]
	if !ok {
		return nil, fmt.Errorf("unknown notify backend %q (%s)", typ, r.registeredTypes())
	}

	return f(params)
}

// TopicPrefix reads "topic_prefix" from the [[notify]] table's parameters,
// falling back to DefaultTopicPrefix when it is absent. It is a Hook-level
// setting, not a backend one, so every backend shares this one way of
// reading it instead of each parsing it out of params on its own.
func TopicPrefix(params map[string]any) string {
	if prefix, ok := params["topic_prefix"].(string); ok && prefix != "" {
		return prefix
	}

	return DefaultTopicPrefix
}

// BuildHook builds the runner.Hook behind the [[notify]] table from the backends
// registered in Default. It has the signature app.NotifyBuilder wants.
func BuildHook(cfg config.Plugin, log *slog.Logger) (runner.Hook, error) {
	pub, err := Default.Build(cfg.Type, cfg.Params)
	if err != nil {
		return nil, err
	}

	return NewHook(pub, TopicPrefix(cfg.Params), log), nil
}

func (r *Registry) registeredTypes() string {
	if len(r.factories) == 0 {
		return "this build has no notify backends compiled in"
	}

	types := make([]string, 0, len(r.factories))
	for typ := range r.factories {
		types = append(types, typ)
	}
	slices.Sort(types)

	return "registered: " + strings.Join(types, ", ")
}
