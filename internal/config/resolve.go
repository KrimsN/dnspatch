package config

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
)

// resolver turns a checked document into a Config. It collects the problems it
// finds in errs, in the order they are reported.
type resolver struct {
	pools

	// definedEvents are the events of each notifier definition, by name.
	definedEvents map[string][]Event

	// global is the polling interval instances fall back to.
	global time.Duration

	// used holds the names of the notifier definitions that at least one
	// instance publishes to.
	used map[string]bool

	// inline holds the notifiers declared in place by an instance, by their
	// generated names.
	inline map[string]Plugin

	errs []error
}

// newResolver reads the events of the notifier definitions.
func newResolver(p pools) *resolver {
	r := &resolver{
		pools:         p,
		definedEvents: make(map[string][]Event, len(p.notifiers)),
		global:        DefaultInterval,
		used:          make(map[string]bool),
		inline:        make(map[string]Plugin),
	}

	for _, name := range slices.Sorted(maps.Keys(p.notifiers)) {
		events, eventErrs := definitionEvents(p.notifiers[name])
		for _, err := range eventErrs {
			r.errs = append(r.errs, fmt.Errorf("notify %q: %w", name, err))
		}

		r.definedEvents[name] = events
	}

	return r
}

// readGlobalInterval takes the top-level "interval" of the document, if any.
func (r *resolver) readGlobalInterval(doc map[string]any) {
	value, ok := doc["interval"]
	if !ok {
		return
	}

	interval, err := parseInterval(value)
	if err != nil {
		r.errs = append(r.errs, err)

		return
	}

	r.global = interval
}

// resolveInstances resolves every instance table. A problem is reported with
// the label of the instance it belongs to.
func (r *resolver) resolveInstances(tables []map[string]any) []Instance {
	var instances []Instance

	seen := make(map[string]bool, len(tables))

	for i, table := range tables {
		in, errs := checkInstance(table)

		if in.Name != "" && seen[in.Name] {
			errs = append(errs, errors.New("name is used by more than one instance"))
		}
		seen[in.Name] = true

		inst, inline, resolveErrs := r.resolveInstance(in)
		errs = append(errs, resolveErrs...)

		r.recordNotify(inst.Notify, inline)

		label := instanceLabel(i, in.Name)
		for _, err := range errs {
			r.errs = append(r.errs, fmt.Errorf("%s: %w", label, err))
		}

		instances = append(instances, inst)
	}

	return instances
}

// recordNotify notes what an instance publishes to: the definitions it names
// and the notifiers it declares in place.
func (r *resolver) recordNotify(refs []NotifyRef, inline map[string]Plugin) {
	for _, ref := range refs {
		if _, declared := inline[ref.Name]; !declared {
			r.used[ref.Name] = true
		}
	}

	maps.Copy(r.inline, inline)
}

// resolveNotifiers resolves the notifier definitions an instance publishes to
// and adds the ones declared in place. Only what an instance publishes to is
// resolved: the environment references of a definition nobody uses need not
// be set. It returns nil when there is no notifier at all.
func (r *resolver) resolveNotifiers() map[string]Plugin {
	notify := make(map[string]Plugin, len(r.used)+len(r.inline))

	for _, name := range slices.Sorted(maps.Keys(r.used)) {
		if !hasKey(r.notifiers, name) {
			continue
		}

		plugin, errs := resolvePluginByRef("notify", "notify", r.notifiers, name, nil)
		r.errs = append(r.errs, errs...)
		notify[name] = plugin
	}

	maps.Copy(notify, r.inline)

	if len(notify) == 0 {
		return nil
	}

	return notify
}

// resolveInstance validates one instance and resolves its references. The
// second result holds the notifiers the instance declares in place, by name.
func (r *resolver) resolveInstance(in rawInstance) (Instance, map[string]Plugin, []error) {
	inst := Instance{Name: in.Name, Interval: r.global}

	var errs []error

	if in.Name == "" {
		errs = append(errs, errors.New(`"name" is required`))
	}

	if in.Interval != nil {
		interval, err := parseInterval(*in.Interval)
		if err != nil {
			errs = append(errs, err)
		} else {
			inst.Interval = interval
		}
	}

	if in.PingURL != nil {
		pingURL, err := expandString(*in.PingURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q: %w", "ping_url", err))
		} else {
			inst.PingURL = pingURL
		}
	}

	var pluginErrs []error

	inst.Retrievers, pluginErrs = resolvePlugins("retriever", r.retrievers, in.Retrievers)
	errs = append(errs, pluginErrs...)

	inst.Providers, pluginErrs = resolvePlugins("provider", r.providers, in.Providers)
	errs = append(errs, pluginErrs...)
	errs = append(errs, duplicateProviders(inst.Providers)...)

	notify, inline, notifyErrs := r.resolveNotify(in.Name, in.Notify)
	errs = append(errs, notifyErrs...)
	inst.Notify = notify

	return inst, inline, errs
}

// resolvePlugins resolves the retriever or provider list of an instance, of
// which there must be at least one. A plugin that fails to resolve stays in
// the result as a zero value, so positions in the list are kept.
func resolvePlugins(kind string, pool map[string]map[string]any, overrides []map[string]any) ([]Plugin, []error) {
	var errs []error

	if len(overrides) == 0 {
		errs = append(errs, fmt.Errorf("at least one %s is required", kind))
	}

	plugins := make([]Plugin, 0, len(overrides))

	for i, override := range overrides {
		plugin, pluginErrs := resolvePlugin(kind, fmt.Sprintf("%s #%d", kind, i+1), pool, override)
		errs = append(errs, pluginErrs...)
		plugins = append(plugins, plugin)
	}

	return plugins, errs
}

func instanceLabel(index int, name string) string {
	if name == "" {
		return fmt.Sprintf("instance #%d", index+1)
	}

	return fmt.Sprintf("instance %q", name)
}

func hasKey(pool map[string]map[string]any, name string) bool {
	_, ok := pool[name]

	return ok
}

// definedNames renders the names in a pool of the given kind for an error
// message.
func definedNames(kind string, pool map[string]map[string]any) string {
	if len(pool) == 0 {
		return "no " + pluralKind(kind) + " are defined"
	}

	return "defined: " + strings.Join(slices.Sorted(maps.Keys(pool)), ", ")
}

// pluralKind names the things a pool of kind holds: "notify" is the key of the
// pool, "notifiers" what it contains.
func pluralKind(kind string) string {
	if kind == "notify" {
		return "notifiers"
	}

	return kind + "s"
}

// duplicateProviders reports providers of one instance that were built from
// the same definition and ended up with the same parameters: they would write
// the same record twice on every tick. A provider that overrides a parameter,
// such as the zone, is a different one. A provider declared inline (no Ref)
// is never flagged: it does not name a shared definition, so there is nothing
// to compare it against.
func duplicateProviders(providers []Plugin) []error {
	var errs []error

	for i, later := range providers {
		if later.Ref == "" {
			continue
		}

		for j, earlier := range providers[:i] {
			if earlier.Ref == later.Ref && reflect.DeepEqual(earlier.Params, later.Params) {
				errs = append(errs, fmt.Errorf("provider #%d repeats provider #%d: same ref %q and same parameters, the record would be written twice",
					i+1, j+1, later.Ref))
				break
			}
		}
	}

	return errs
}
