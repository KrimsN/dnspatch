package runner

import (
	"context"
	"net/netip"
	"time"
)

// EventKind says what an Event reports.
type EventKind int

const (
	// KindInstanceStatus means the instance as a whole started failing or recovered.
	KindInstanceStatus EventKind = iota + 1
	// KindProviderStatus means one provider started failing or recovered.
	KindProviderStatus
	// KindRetrieverStatus means one retriever started failing or recovered.
	KindRetrieverStatus
	// KindIPChange means addresses were written to providers and differ from the
	// previous ones.
	KindIPChange
	// KindCycle means a cycle completed.
	KindCycle
	// KindStarted means the instance began running.
	KindStarted
	// KindStopped means the instance finished running.
	KindStopped
)

// Event is a fact the runner reports about an instance. Which fields are set
// depends on Kind.
type Event struct {
	Kind     EventKind
	Instance string
	// Time is when the event happened, by the runner's clock.
	Time time.Time

	// Failed is the new state for the three status kinds: true for a failure,
	// false for a recovery. For KindCycle it says the cycle failed.
	Failed bool
	// Err is the error behind a failure: of the cycle for KindInstanceStatus and
	// KindCycle, of the provider or retriever for the other status kinds. Nil
	// when Failed is false.
	Err error
	// Name is the provider or retriever of a status event.
	Name string
	// Changes lists, for KindIPChange, every address written this cycle that
	// differs from the previous one.
	Changes []AddressChange
	// Version is the version of the daemon, for KindStarted and KindStopped.
	Version string
}

// AddressChange is one address written to a provider.
type AddressChange struct {
	Provider string
	// IPv6 is the family of the address.
	IPv6 bool
	// Old is the previous address, or the zero value when it is not known: the
	// first write after the start or after a failed write.
	Old, New netip.Addr
}

// EventHook is implemented by a Hook that wants to know more than that a cycle
// completed. It is optional and matched structurally: the runner passes every
// Event to the hooks of an instance that have it, and a hook that does not is
// only given AfterCycle.
//
// Like AfterCycle, OnEvent runs inline in the instance's goroutine, so it must
// bound its own work with a timeout and do its own logging.
//
// The context of every event except KindStopped is cancelled when the daemon
// is stopping. Events caused by the stop itself are not sent at all; KindStopped
// gets a context that is not cancelled, so that it can still be published.
type EventHook interface {
	OnEvent(ctx context.Context, ev Event)
}

// trackedState remembers whether something last worked or failed, to report
// only the transitions.
type trackedState struct {
	known  bool
	failed bool
}

// observe records the outcome of an attempt and reports whether it is a
// transition worth an event: any change of state, and, the first time the
// thing is seen, only a failure: a first success is not news.
func (s *trackedState) observe(failed bool) bool {
	was, known := s.failed, s.known
	s.known, s.failed = true, failed

	if !known {
		return failed
	}

	return was != failed
}
