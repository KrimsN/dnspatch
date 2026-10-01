package config

import (
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
			want: []string{`instance "a": notify #1: "ref" is required`},
		},
		"inline notifier": {
			doc: header + definition + notifyInstance("a", `notify = [{ type = "redis", address = "x" }]
`),
			want: []string{
				`instance "a": notify #1: unknown key "address"`,
				`instance "a": notify #1: unknown key "type"`,
				`"ref" is required`,
			},
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
