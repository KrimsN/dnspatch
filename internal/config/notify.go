package config

import (
	"fmt"
	"maps"
	"slices"
)

// resolveNotify picks the notifiers of an instance from the entries it lists:
// every one it names must be defined, and none twice. An entry's own events
// take precedence over its definition's. An instance that lists none at all,
// as opposed to an empty list, publishes to every definition. An entry that
// declares its notifier in place is named after the instance, the type and its
// position in the list ("home/redis#2") and returned as a plugin of its own;
// it is never shared with another instance.
func (r *resolver) resolveNotify(instance string, entries []rawNotify) ([]NotifyRef, map[string]Plugin, []error) {
	if entries == nil {
		return r.allNotifyRefs(), nil, nil
	}

	var errs []error

	refs := make([]NotifyRef, 0, len(entries))
	inline := make(map[string]Plugin)
	seen := make(map[string]bool, len(entries))

	for i, entry := range entries {
		switch {
		case entry.Inline != nil:
			name, plugin, pluginErrs := resolveInlineNotify(instance, i, entry, r.notifiers)
			errs = append(errs, pluginErrs...)

			if len(pluginErrs) == 0 {
				inline[name] = plugin
				refs = append(refs, NotifyRef{Name: name, Events: eventsOrDefault(entry.Events)})
			}
		case entry.Name == "":
			// Already reported when the list was checked.
		default:
			ref, err := r.namedNotify(entry, seen)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			refs = append(refs, ref)
		}
	}

	return refs, inline, errs
}

// allNotifyRefs lists every notifier definition with its own events.
func (r *resolver) allNotifyRefs() []NotifyRef {
	refs := make([]NotifyRef, 0, len(r.notifiers))
	for _, name := range slices.Sorted(maps.Keys(r.notifiers)) {
		refs = append(refs, NotifyRef{Name: name, Events: r.definedEvents[name]})
	}

	return refs
}

// namedNotify checks an entry that names a notifier definition and marks the
// name in seen. The entry's own events take precedence over the definition's.
func (r *resolver) namedNotify(entry rawNotify, seen map[string]bool) (NotifyRef, error) {
	switch {
	case !hasKey(r.notifiers, entry.Name):
		return NotifyRef{}, fmt.Errorf("notify %q is not defined (%s)", entry.Name, definedNames("notify", r.notifiers))
	case seen[entry.Name]:
		return NotifyRef{}, fmt.Errorf("notify %q is listed twice", entry.Name)
	}

	seen[entry.Name] = true

	events := entry.Events
	if events == nil {
		events = r.definedEvents[entry.Name]
	}

	return NotifyRef{Name: entry.Name, Events: events}, nil
}

// resolveInlineNotify builds the plugin of the notifier an instance declares
// in place, at position index of its notify list, and returns it with its
// generated name.
func resolveInlineNotify(instance string, index int, entry rawNotify, notifiers map[string]map[string]any) (string, Plugin, []error) {
	where := fmt.Sprintf("notify #%d", index+1)

	plugin, errs := resolvePluginInline("notify", where, entry.Inline)
	if len(errs) > 0 {
		return "", Plugin{}, errs
	}

	name := fmt.Sprintf("%s/%s#%d", instance, plugin.Type, index+1)
	if hasKey(notifiers, name) {
		return "", Plugin{}, []error{fmt.Errorf("%s: the generated name %q is taken by a [notify.<name>] definition, rename it", where, name)}
	}

	plugin.Ref = name

	return name, plugin, nil
}

// eventsOrDefault is events, or a copy of the default set when there are none:
// a notifier declared in place has no definition to take them from.
func eventsOrDefault(events []Event) []Event {
	if events == nil {
		return slices.Clone(defaultEvents)
	}

	return events
}
