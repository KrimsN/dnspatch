// Package config parses the TOML configuration: named retriever, provider and
// notifier definitions, instances and their polling intervals.
//
// A file holds pools of named definitions ([retriever.<name>],
// [provider.<name>] and [notify.<name>], each with a "type") and a list of
// [[instance]] tables. An instance refers to retriever and provider
// definitions by "ref" and may override any of their parameters, and lists the
// notifiers it publishes to by name. Parse resolves every reference, so the
// result holds, per instance, the plugin type and the final parameters ready
// for the plugin registry.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultInterval is used when neither the file nor the instance sets one.
	DefaultInterval = 5 * time.Minute

	// MinInterval is the shortest accepted polling interval.
	MinInterval = time.Second
)

// Config is a parsed and fully resolved configuration.
type Config struct {
	// Interval is the polling interval instances fall back to.
	Interval  time.Duration
	Instances []Instance
	// Notify holds the notifiers, by name, that at least one instance publishes
	// to: the [notify.<name>] definitions it lists or defaults to, and the ones
	// it declares in place, named "<instance>/<type>#<position in its notify
	// list>". They receive an instance's success/failure transitions. A build
	// that has no notifier rejects a config that uses one, the same way an
	// unsupported PingURL is rejected. Each entry is one broker connection,
	// shared by every instance that lists it; two of them may be of one type,
	// for example two Redis servers. A definition no instance uses is not
	// resolved.
	Notify map[string]Plugin
}

// Instance ties one or more retrievers to one or more providers. Retrievers
// are polled in order until every address family is filled: a later one
// reporting a family an earlier one already filled is a redundant fallback
// source, not an error.
type Instance struct {
	Name string
	// Interval is the polling interval, already resolved against the global one.
	Interval   time.Duration
	Retrievers []Plugin
	Providers  []Plugin
	// PingURL, when set, is called on every completed cycle of this instance
	// by a build that supports monitoring hooks (the ping build tag); a build
	// that does not rejects a config that sets it rather than silently
	// ignoring it. Environment and file references ("${NAME}", "${file:PATH}")
	// are expanded, same as a plugin parameter, since the URL commonly embeds
	// a secret token (Healthchecks.io, Uptime Kuma).
	PingURL string
	// Notify lists the notifiers this instance publishes to, each with the event
	// types that reach it, and each named by a key of Config.Notify. An instance
	// that does not say gets all the notifiers the file defines, with their own
	// events, and one that says "notify = []" has none.
	Notify []NotifyRef
}

// Plugin is a plugin type together with its final parameters: the named
// definition overlaid with the instance override. The service keys "type"
// and "ref" are not part of Params, and environment references are expanded.
type Plugin struct {
	// Ref is the name of the definition the plugin was built from, or empty
	// for a plugin declared inline with "type" instead of "ref".
	Ref    string
	Type   string
	Params map[string]any
}

// Name is how messages and logs call the plugin: the name of the definition it
// was built from, or its type for a plugin declared inline.
func (p Plugin) Name() string {
	if p.Ref != "" {
		return p.Ref
	}

	return p.Type
}

// Load reads and parses the config file at path. Problems are listed under a
// header line naming the file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("config %s:\n%w", path, err)
	}

	return cfg, nil
}

// Parse parses TOML config data. Problems are reported together, one per
// line, each naming the instance, plugin and parameter it belongs to.
func Parse(data []byte) (Config, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return Config{}, err
	}

	sh, shapeErrs := checkShape(doc)
	if len(shapeErrs) > 0 {
		return Config{}, errors.Join(shapeErrs...)
	}

	r := newResolver(sh.pools)
	r.readGlobalInterval(doc)

	if len(sh.instances) == 0 {
		r.errs = append(r.errs, errors.New("no instances defined"))
	}

	cfg := Config{Instances: r.resolveInstances(sh.instances)}
	cfg.Notify = r.resolveNotifiers()
	cfg.Interval = r.global

	if len(r.errs) > 0 {
		return Config{}, errors.Join(r.errs...)
	}

	return cfg, nil
}
