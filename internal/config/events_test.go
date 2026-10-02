package config

import (
	"fmt"
	"slices"
	"testing"
)

// notifyEvents maps the notifiers of an instance to their events, in strings.
func notifyEvents(refs []NotifyRef) map[string][]string {
	got := make(map[string][]string, len(refs))

	for _, ref := range refs {
		events := make([]string, len(ref.Events))
		for i, e := range ref.Events {
			events[i] = string(e)
		}

		got[ref.Name] = events
	}

	return got
}

func wantEvents(t *testing.T, refs []NotifyRef, name string, want ...string) {
	t.Helper()

	got, ok := notifyEvents(refs)[name]
	if !ok {
		t.Fatalf("notify %q is missing from %+v", name, refs)
	}

	if !slices.Equal(got, want) {
		t.Errorf("events of %q = %v, want %v", name, got, want)
	}
}

func TestNotifyEventsDefaultToStatus(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type = "redis"
`+notifyInstance("a", ""))

	wantEvents(t, cfg.Instances[0].Notify, "alerts", "status")
}

func TestNotifyEventsOfTheDefinition(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type   = "redis"
events = ["status", "provider_status", "retriever_status", "ip_change", "cycle", "lifecycle"]

[notify.audit]
type   = "rabbitmq"
events = ["cycle", "lifecycle"]
`+notifyInstance("a", ""))

	wantEvents(t, cfg.Instances[0].Notify, "alerts", "status", "provider_status", "retriever_status", "ip_change", "cycle", "lifecycle")
	wantEvents(t, cfg.Instances[0].Notify, "audit", "cycle", "lifecycle")
}

// "events" belongs to the configuration: a plugin must not see it, or it would
// have to know every notifier's setting by name.
func TestNotifyEventsAreNotPluginParameters(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type    = "redis"
address = "redis://localhost"
events  = ["cycle"]
`+notifyInstance("a", ""))

	params := cfg.Notify["alerts"].Params
	if _, ok := params["events"]; ok {
		t.Errorf("Params = %v, want no events", params)
	}

	if params["address"] != "redis://localhost" {
		t.Errorf("Params = %v, want the address kept", params)
	}
}

func TestNotifyOverrideInInlineArray(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type   = "redis"
events = ["status", "ip_change"]

[notify.audit]
type   = "rabbitmq"
events = ["cycle"]
`+notifyInstance("a", `notify = ["audit", { ref = "alerts", events = ["lifecycle"] }]
`)+notifyInstance("b", `notify = ["alerts", { ref = "audit" }]
`))

	a := cfg.Instances[0].Notify
	if got := notifyNames(a); !slices.Equal(got, []string{"audit", "alerts"}) {
		t.Errorf("instance a notify = %v, want the order of the list", got)
	}

	wantEvents(t, a, "audit", "cycle")
	wantEvents(t, a, "alerts", "lifecycle")

	b := cfg.Instances[1].Notify
	wantEvents(t, b, "alerts", "status", "ip_change")
	wantEvents(t, b, "audit", "cycle")
}

func TestNotifyOverrideInTableHeaders(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type   = "redis"
events = ["status", "ip_change"]

[notify.audit]
type = "rabbitmq"
`+notifyInstance("a", `
[[instance.notify]]
ref    = "alerts"
events = ["status"]

[[instance.notify]]
ref = "audit"
`))

	n := cfg.Instances[0].Notify
	wantEvents(t, n, "alerts", "status")
	wantEvents(t, n, "audit", "status")
}

// Two instances overriding one definition differently still share the
// definition: it is resolved once.
func TestNotifyOverrideDoesNotTouchTheDefinition(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type   = "redis"
events = ["status", "ip_change"]
`+notifyInstance("a", `notify = [{ ref = "alerts", events = ["cycle"] }]
`)+notifyInstance("b", `notify = ["alerts"]
`))

	wantEvents(t, cfg.Instances[0].Notify, "alerts", "cycle")
	wantEvents(t, cfg.Instances[1].Notify, "alerts", "status", "ip_change")

	if len(cfg.Notify) != 1 {
		t.Errorf("Notify = %+v, want one shared definition", cfg.Notify)
	}
}

func TestNotifyEventsValidation(t *testing.T) {
	const definition = `
[notify.alerts]
type = "redis"
`

	tests := map[string]struct {
		doc  string
		want []string
	}{
		"unknown in the definition": {
			doc: header + `
[notify.alerts]
type   = "redis"
events = ["status", "backoff"]
` + notifyInstance("a", ""),
			want: []string{`notify "alerts": unknown event "backoff" (valid: status, provider_status, retriever_status, ip_change, cycle, lifecycle)`},
		},
		"empty in the definition": {
			doc: header + `
[notify.alerts]
type   = "redis"
events = []
` + notifyInstance("a", ""),
			want: []string{`notify "alerts": "events" must not be empty`},
		},
		"repeated in the definition": {
			doc: header + `
[notify.alerts]
type   = "redis"
events = ["cycle", "cycle"]
` + notifyInstance("a", ""),
			want: []string{`notify "alerts": event "cycle" is listed twice`},
		},
		"wrong type in the definition": {
			doc: header + `
[notify.alerts]
type   = "redis"
events = "status"
` + notifyInstance("a", ""),
			want: []string{`notify "alerts": "events" must be an array of strings`},
		},
		"not a string in the definition": {
			doc: header + `
[notify.alerts]
type   = "redis"
events = [1]
` + notifyInstance("a", ""),
			want: []string{`"events"[0] must be a string`},
		},
		"unknown in the override": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = "alerts", events = ["nope"] }]
`),
			want: []string{`instance "a": notify #1: unknown event "nope"`},
		},
		"empty in the override": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = "alerts", events = [] }]
`),
			want: []string{`instance "a": notify #1: "events" must not be empty`},
		},
		"repeated in the override": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = "alerts", events = ["status", "status"] }]
`),
			want: []string{`instance "a": notify #1: event "status" is listed twice`},
		},
		"undefined ref": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = "nope", events = ["status"] }]
`),
			want: []string{`instance "a": notify "nope" is not defined (defined: alerts)`},
		},
		"definition listed twice": {
			doc: header + definition + notifyInstance("a", `notify = ["alerts", { ref = "alerts", events = ["cycle"] }]
`),
			want: []string{`instance "a": notify "alerts" is listed twice`},
		},
		"no ref": {
			doc: header + definition + notifyInstance("a", `notify = [{ events = ["status"] }]
`),
			want: []string{`instance "a": notify #1: either "ref" or "type" is required`},
		},
		"ref and type": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = "alerts", type = "redis" }]
`),
			want: []string{`instance "a": notify #1: "ref" and "type" cannot both be set`},
		},
		"inline type is not a string": {
			doc: header + definition + notifyInstance("a", `notify = [{ type = 1 }]
`),
			want: []string{`instance "a": notify #1: "type" must be a string`},
		},
		"inline events are checked": {
			doc: header + definition + notifyInstance("a", `notify = [{ type = "redis", events = ["nope"] }]
`),
			want: []string{`instance "a": notify #1: unknown event "nope"`},
		},
		"inline name is taken": {
			doc: header + `
[notify."a/redis#1"]
type = "redis"
` + notifyInstance("a", `notify = [{ type = "redis" }]
`),
			want: []string{`instance "a": notify #1: the generated name "a/redis#1" is taken`},
		},
		"connection setting in the override": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = "alerts", address = "redis://other" }]
`),
			want: []string{`instance "a": notify #1: unknown key "address": only "ref" and "events" are allowed`},
		},
		"extra key in table headers": {
			doc: header + definition + notifyInstance("a", `
[[instance.notify]]
ref        = "alerts"
topic_prefix = "x"
`),
			want: []string{`instance "a": notify #1: unknown key "topic_prefix"`},
		},
		"ref is not a string": {
			doc: header + definition + notifyInstance("a", `notify = [{ ref = 1 }]
`),
			want: []string{`instance "a": notify #1: "ref" must be the name of a [notify.<name>] definition`},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			wantContains(t, parseError(t, tc.doc), tc.want...)
		})
	}
}

// A notifier declared in place takes every parameter of its plugin, has a name
// made of the instance, the type and its position, and is not shared.
func TestInlineNotify(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")

	cfg := mustParse(t, header+`
[notify.alerts]
type = "redis"
`+notifyInstance("a", `notify = ["alerts", { type = "redis", address = "${REDIS_URL}", events = ["cycle"] }, { type = "redis" }]
`)+notifyInstance("b", `notify = [{ type = "redis" }]
`))

	a := cfg.Instances[0].Notify
	if got, want := notifyNames(a), []string{"alerts", "a/redis#2", "a/redis#3"}; !slices.Equal(got, want) {
		t.Fatalf("instance a notify = %q, want %q", got, want)
	}

	wantEvents(t, a, "a/redis#2", "cycle")
	wantEvents(t, a, "a/redis#3", "status")

	plugin := cfg.Notify["a/redis#2"]
	if plugin.Type != "redis" || plugin.Name() != "a/redis#2" {
		t.Errorf("Notify[a/redis#2] = %+v, want type redis named after the instance", plugin)
	}

	if got := plugin.Params["address"]; got != "redis://localhost:6379/0" {
		t.Errorf(`Params["address"] = %v, want the expanded URL`, got)
	}

	if _, ok := plugin.Params["events"]; ok {
		t.Errorf("Params = %v, want no events: they belong to the configuration", plugin.Params)
	}

	if got, want := notifyNames(cfg.Instances[1].Notify), []string{"b/redis#1"}; !slices.Equal(got, want) {
		t.Errorf("instance b notify = %q, want %q", got, want)
	}

	if len(cfg.Notify) != 4 {
		t.Errorf("Notify has %d entries, want alerts and the three declared in place", len(cfg.Notify))
	}
}

// An instance that does not say gets the definitions of the file, not the
// notifiers another instance declares in place.
func TestInlineNotifyIsNotInherited(t *testing.T) {
	cfg := mustParse(t, header+`
[notify.alerts]
type = "redis"
`+notifyInstance("a", `notify = [{ type = "redis" }]
`)+notifyInstance("b", ""))

	if got, want := notifyNames(cfg.Instances[1].Notify), []string{"alerts"}; !slices.Equal(got, want) {
		t.Errorf("instance b notify = %q, want %q", got, want)
	}
}

func TestInlineNotifyReportsEnvironmentErrors(t *testing.T) {
	wantContains(t, parseError(t, header+notifyInstance("a", `notify = [{ type = "redis", address = "${NOTIFY_NOT_SET}" }]
`)), `NOTIFY_NOT_SET`)
}

// Every problem is reported at once, not only the first.
func TestNotifyEventsErrorsAreCollected(t *testing.T) {
	got := parseError(t, header+`
[notify.alerts]
type   = "redis"
events = ["nope"]

[notify.audit]
type   = "redis"
events = []
`+notifyInstance("a", `notify = [{ ref = "alerts", events = ["status", "status"] }, { ref = "audit", events = ["nope"] }]
`))

	wantContains(t, got,
		`notify "alerts": unknown event "nope"`,
		`notify "audit": "events" must not be empty`,
		`instance "a": notify #1: event "status" is listed twice`,
		`instance "a": notify #2: unknown event "nope"`,
	)
}

func TestEventStringIsItsName(t *testing.T) {
	for _, e := range allEvents {
		if got := fmt.Sprint(e); got != string(e) {
			t.Errorf("fmt.Sprint(%q) = %q, want the name itself", string(e), got)
		}
	}
}
