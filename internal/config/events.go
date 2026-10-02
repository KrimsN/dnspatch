package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Event is a type of event a notifier can publish, as written in the "events"
// list of a [notify.<name>] definition or of an instance's notify entry.
type Event string

// The event types. Which of them a notifier publishes is decided here, in the
// configuration; the notifier plugin itself only transports the payload.
const (
	// EventStatus is the success/failure of an instance as a whole.
	EventStatus Event = "status"
	// EventProviderStatus is the success/failure of one provider.
	EventProviderStatus Event = "provider_status"
	// EventRetrieverStatus is the success/failure of one retriever.
	EventRetrieverStatus Event = "retriever_status"
	// EventIPChange is an address written to a provider that differs from the
	// previous one.
	EventIPChange Event = "ip_change"
	// EventCycle is a completed cycle, successful or not.
	EventCycle Event = "cycle"
	// EventLifecycle is the start and the stop of an instance.
	EventLifecycle Event = "lifecycle"
)

// allEvents lists every valid event type, in the order the documentation uses.
var allEvents = []Event{EventStatus, EventProviderStatus, EventRetrieverStatus, EventIPChange, EventCycle, EventLifecycle}

// defaultEvents is what a notifier publishes when its definition has no
// "events": the behavior before event types existed.
var defaultEvents = []Event{EventStatus}

// valid reports whether e is one of the known event types.
func (e Event) valid() bool {
	return slices.Contains(allEvents, e)
}

// String implements fmt.Stringer.
func (e Event) String() string { return string(e) }

// validEventNames renders the valid event types for an error message.
func validEventNames() string {
	names := make([]string, len(allEvents))
	for i, e := range allEvents {
		names[i] = string(e)
	}

	return strings.Join(names, ", ")
}

// NotifyRef is a notifier an instance publishes to, with the event types that
// reach it from this instance.
type NotifyRef struct {
	// Name is the name of the [notify.<name>] definition.
	Name string
	// Events is the final set of event types: the override in the instance if
	// it has one, otherwise the definition's, otherwise the default.
	Events []Event
}

// rawNotify is one entry of an instance's notify list after its shape has been
// checked. Events is nil when the entry does not override the definition's.
// Inline is the plugin table of an entry that declares its notifier itself
// ("type" instead of "ref"), without "events"; it is nil for a reference.
type rawNotify struct {
	Name   string
	Events []Event
	Inline map[string]any
}

// parseEvents converts the value of an "events" key. Every problem is
// reported: a wrong type, an empty list, an unknown or repeated type.
func parseEvents(value any) ([]Event, []error) {
	items, ok := value.([]any)
	if !ok {
		return nil, []error{fmt.Errorf(`"events" must be an array of strings, for example ["status", "ip_change"] (valid: %s)`, validEventNames())}
	}

	if len(items) == 0 {
		return nil, []error{errors.New(`"events" must not be empty: a notifier with no events publishes nothing, so leave it out of the instance's "notify" instead`)}
	}

	events := make([]Event, 0, len(items))

	var errs []error

	for i, item := range items {
		name, ok := item.(string)
		if !ok {
			errs = append(errs, fmt.Errorf(`"events"[%d] must be a string, not %T`, i, item))
			continue
		}

		event := Event(name)

		switch {
		case !event.valid():
			errs = append(errs, fmt.Errorf("unknown event %q (valid: %s)", name, validEventNames()))
		case slices.Contains(events, event):
			errs = append(errs, fmt.Errorf("event %q is listed twice", name))
		default:
			events = append(events, event)
		}
	}

	return events, errs
}

// checkNotifyList converts the notify list of an instance. Every entry is the
// name of a definition, a table with a "ref" and optionally an "events" to
// override the definition's for this instance only, or a table with a "type"
// that declares a notifier of its own; they may be mixed in an inline array.
func checkNotifyList(value any) ([]rawNotify, []error) {
	tables, errs := asRefs("notify", value)

	notify := make([]rawNotify, 0, len(tables))

	for i, table := range tables {
		where := fmt.Sprintf("notify #%d", i+1)

		entry, entryErrs := checkNotifyTable(table)
		for _, err := range entryErrs {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		}

		notify = append(notify, entry)
	}

	return notify, errs
}

// checkNotifyTable checks one table of an instance's notify list. With a "ref"
// only "ref" and "events" are allowed: the connection to the broker belongs to
// the definition and is shared by every instance that uses it, so an instance
// cannot reconfigure it. With a "type" the table is a definition of its own
// and takes every parameter of the plugin.
func checkNotifyTable(table map[string]any) (rawNotify, []error) {
	_, hasRef := table["ref"]
	_, hasType := table["type"]

	switch {
	case hasRef && hasType:
		return rawNotify{}, []error{errors.New(`"ref" and "type" cannot both be set: a notifier is either a reference to [notify.<name>] or declared in place`)}
	case hasType:
		return checkInlineNotify(table)
	case hasRef:
		return checkNotifyRef(table)
	default:
		return rawNotify{}, []error{errors.New(`either "ref" or "type" is required`)}
	}
}

// checkNotifyRef checks a table that refers to a [notify.<name>] definition.
func checkNotifyRef(table map[string]any) (rawNotify, []error) {
	var (
		entry rawNotify
		errs  []error
	)

	for _, key := range slices.Sorted(maps.Keys(table)) {
		if key != "ref" && key != "events" {
			errs = append(errs, fmt.Errorf(`unknown key %q: only "ref" and "events" are allowed next to "ref", the connection is set in [notify.<name>] and shared by every instance that uses it; to configure it here, declare the notifier with "type" instead`, key))
		}
	}

	if name, ok := table["ref"].(string); !ok || name == "" {
		errs = append(errs, errors.New(`"ref" must be the name of a [notify.<name>] definition`))
	} else {
		entry.Name = name
	}

	var eventErrs []error

	entry.Events, eventErrs = optionalEvents(table)
	errs = append(errs, eventErrs...)

	return entry, errs
}

// checkInlineNotify checks a table that declares a notifier in place. Its
// parameters are checked later, by the plugin that receives them.
func checkInlineNotify(table map[string]any) (rawNotify, []error) {
	var errs []error

	if typ, ok := table["type"].(string); !ok || typ == "" {
		errs = append(errs, errors.New(`"type" must be a string`))
	}

	entry := rawNotify{Inline: make(map[string]any, len(table))}

	for key, value := range table {
		if key != "events" {
			entry.Inline[key] = value
		}
	}

	var eventErrs []error

	entry.Events, eventErrs = optionalEvents(table)
	errs = append(errs, eventErrs...)

	return entry, errs
}

// optionalEvents reads the "events" of a table, nil when it has none.
func optionalEvents(table map[string]any) ([]Event, []error) {
	value, ok := table["events"]
	if !ok {
		return nil, nil
	}

	return parseEvents(value)
}

// definitionEvents reads the "events" of a [notify.<name>] definition,
// falling back to the default.
func definitionEvents(definition map[string]any) ([]Event, []error) {
	value, ok := definition["events"]
	if !ok {
		return slices.Clone(defaultEvents), nil
	}

	return parseEvents(value)
}
