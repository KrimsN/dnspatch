package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"time"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
)

// publishTimeout bounds one publication. A hook runs inline in the goroutine
// of its instance and one cycle may produce several events, so a broker that
// does not answer must not hold the instance for long.
const publishTimeout = 5 * time.Second

// Severity of an event. It is fixed per event and only told to the receiver.
const (
	severityInfo    = "info"
	severityWarning = "warning"
	severityError   = "error"
)

// States of the status events and of lifecycle.
const (
	stateFailure  = "failure"
	stateRecovery = "recovery"
	stateStarted  = "started"
	stateStopped  = "stopped"
)

// header holds the fields every payload has.
type header struct {
	Event    config.Event `json:"event"`
	Severity string       `json:"severity"`
	Instance string       `json:"instance"`
	Time     time.Time    `json:"time"`
}

// statusPayload is the payload of EventStatus. The fields before 0.5 (instance,
// success, error, time) are all here.
type statusPayload struct {
	header
	State   string `json:"state"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type providerStatusPayload struct {
	header
	Provider string `json:"provider"`
	State    string `json:"state"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

type retrieverStatusPayload struct {
	header
	Retriever string `json:"retriever"`
	State     string `json:"state"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

type ipChangePayload struct {
	header
	Changes []change `json:"changes"`
}

type change struct {
	Provider string `json:"provider"`
	Family   string `json:"family"`
	Old      string `json:"old"`
	New      string `json:"new"`
}

type cyclePayload struct {
	header
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type lifecyclePayload struct {
	header
	State   string `json:"state"`
	Version string `json:"version"`
}

// Hook is a runner.EventHook that publishes the events of one instance, those
// whose type is in its set, through a notifier. Which types an instance wants
// from a notifier is configuration, so the notifier itself stays a plain
// transport.
//
// Several Hooks, one per instance, share the connection of a definition; see
// Connection.
type Hook struct {
	pub     plugin.Notifier
	log     *slog.Logger
	events  map[config.Event]bool
	timeout time.Duration
}

// newHook returns a Hook that publishes events of the given types through pub,
// under the name of the instance; pub adds the topic prefix.
func newHook(pub plugin.Notifier, log *slog.Logger, events []config.Event) *Hook {
	set := make(map[config.Event]bool, len(events))
	for _, e := range events {
		set[e] = true
	}

	return &Hook{pub: pub, log: log, events: set, timeout: publishTimeout}
}

// AfterCycle implements runner.Hook. Everything the notifier publishes comes
// through OnEvent, including the cycle itself, so there is nothing to do here.
func (h *Hook) AfterCycle(context.Context, runner.CycleEvent) {}

// OnEvent implements runner.EventHook.
func (h *Hook) OnEvent(ctx context.Context, ev runner.Event) {
	event, payload := describe(ev)
	if !h.events[event] {
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		h.log.Warn("could not encode notify event", "instance", ev.Instance, "event", event, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	if err := h.pub.Publish(ctx, ev.Instance, data); err != nil {
		h.log.Warn("could not publish notify event", "instance", ev.Instance, "event", event, "err", err)
	}
}

// describe turns an event of the runner into its type and payload.
func describe(ev runner.Event) (config.Event, any) {
	head := func(event config.Event, severity string) header {
		return header{Event: event, Severity: severity, Instance: ev.Instance, Time: ev.Time}
	}

	switch ev.Kind {
	case runner.KindInstanceStatus:
		state, severity := transition(ev, severityError)

		return config.EventStatus, statusPayload{
			header: head(config.EventStatus, severity), State: state, Success: !ev.Failed, Error: errText(ev.Err),
		}
	case runner.KindProviderStatus:
		state, severity := transition(ev, severityError)

		return config.EventProviderStatus, providerStatusPayload{
			header: head(config.EventProviderStatus, severity), Provider: ev.Name, State: state, Success: !ev.Failed, Error: errText(ev.Err),
		}
	case runner.KindRetrieverStatus:
		// Another retriever may still serve the cycle; if none does, the cycle
		// itself reports an error.
		state, severity := transition(ev, severityWarning)

		return config.EventRetrieverStatus, retrieverStatusPayload{
			header: head(config.EventRetrieverStatus, severity), Retriever: ev.Name, State: state, Success: !ev.Failed, Error: errText(ev.Err),
		}
	case runner.KindIPChange:
		changes := make([]change, len(ev.Changes))
		for i, c := range ev.Changes {
			changes[i] = change{Provider: c.Provider, Family: family(c), Old: addrText(c.Old), New: addrText(c.New)}
		}

		return config.EventIPChange, ipChangePayload{header: head(config.EventIPChange, severityInfo), Changes: changes}
	case runner.KindCycle:
		severity := severityInfo
		if ev.Failed {
			severity = severityError
		}

		return config.EventCycle, cyclePayload{
			header: head(config.EventCycle, severity), Success: !ev.Failed, Error: errText(ev.Err),
		}
	case runner.KindStarted:
		return config.EventLifecycle, lifecyclePayload{header: head(config.EventLifecycle, severityInfo), State: stateStarted, Version: ev.Version}
	case runner.KindStopped:
		return config.EventLifecycle, lifecyclePayload{header: head(config.EventLifecycle, severityInfo), State: stateStopped, Version: ev.Version}
	default:
		return "", nil
	}
}

// transition names the state of a status event and its severity: failureSeverity
// for a failure, info for a recovery.
func transition(ev runner.Event, failureSeverity string) (state, severity string) {
	if ev.Failed {
		return stateFailure, failureSeverity
	}

	return stateRecovery, severityInfo
}

func family(c runner.AddressChange) string {
	if c.IPv6 {
		return "ipv6"
	}

	return "ipv4"
}

// addrText renders an address, or an empty string for one that is not known.
func addrText(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}

	return a.String()
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
