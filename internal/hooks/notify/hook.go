package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
)

// Event is the JSON payload published for one instance whose success/failure
// status just changed.
type Event struct {
	Instance string    `json:"instance"`
	Success  bool      `json:"success"`
	Error    string    `json:"error,omitempty"`
	Time     time.Time `json:"time"`
}

// Hook is a runner.Hook that publishes an Event through pub whenever an
// instance's status flips between success and failure, instead of on every
// cycle: a notification channel is for changes worth a human's attention,
// unlike the ping hook's dead man's switch, which needs a heartbeat on every
// cycle to work at all.
//
// The very first cycle of an instance only publishes if it fails: a first
// cycle that succeeds is the normal start of the day, not news. One Hook
// value is shared across every instance, so it tracks each one's last known
// status itself.
type Hook struct {
	pub plugin.Notifier
	log *slog.Logger

	mu    sync.Mutex
	state map[string]bool // instance -> last known Success
}

// NewHook returns a Hook that publishes through pub, under the name of the
// instance; pub adds the topic prefix.
func NewHook(pub plugin.Notifier, log *slog.Logger) *Hook {
	return &Hook{
		pub:   pub,
		log:   log,
		state: make(map[string]bool),
	}
}

// Close releases the connection of the notifier the Hook publishes through. The
// daemon closes every hook that has it when it stops.
func (h *Hook) Close() error {
	return h.pub.Close()
}

// AfterCycle implements runner.Hook.
func (h *Hook) AfterCycle(ctx context.Context, ev runner.CycleEvent) {
	if !h.changed(ev) {
		return
	}

	payload, err := json.Marshal(Event{
		Instance: ev.Instance,
		Success:  ev.Success,
		Error:    errText(ev.Err),
		Time:     time.Now(),
	})
	if err != nil {
		h.log.Warn("could not encode notify event", "instance", ev.Instance, "err", err)
		return
	}

	if err := h.pub.Publish(ctx, ev.Instance, payload); err != nil {
		h.log.Warn("could not publish notify event", "instance", ev.Instance, "err", err)
	}
}

// changed reports whether ev is worth publishing: the instance's very first
// recorded cycle failing, or a flip from the last cycle that was published.
// It also records ev's status as the new last-known one.
func (h *Hook) changed(ev runner.CycleEvent) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	last, known := h.state[ev.Instance]
	h.state[ev.Instance] = ev.Success

	if !known {
		return !ev.Success
	}

	return last != ev.Success
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
