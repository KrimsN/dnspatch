package runner

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/dnspatch/dnspatch/plugin"
)

// eventRecorder is a hook that takes events, and remembers each one in order,
// with whether the context it came with was already cancelled.
type eventRecorder struct {
	mu        sync.Mutex
	events    []Event
	cancelled []bool
	cycles    []CycleEvent
}

func (r *eventRecorder) AfterCycle(_ context.Context, ev CycleEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cycles = append(r.cycles, ev)
}

func (r *eventRecorder) OnEvent(ctx context.Context, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	r.cancelled = append(r.cancelled, ctx.Err() != nil)
}

func (r *eventRecorder) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// of returns the events of one kind, in order.
func (r *eventRecorder) of(kind EventKind) []Event {
	var out []Event
	for _, ev := range r.all() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func (r *eventRecorder) kinds() []EventKind {
	var out []EventKind
	for _, ev := range r.all() {
		out = append(out, ev.Kind)
	}
	return out
}

// newEventInstance is newTestInstanceMulti with an event recorder as its hook.
func newEventInstance(clock Clock, retrievers []*fakeRetriever, providers ...*fakeProvider) (*instance, *eventRecorder) {
	rec := &eventRecorder{}
	cfg := Instance{Name: "test", Interval: testInterval, Hooks: []Hook{rec}}
	for i, r := range retrievers {
		cfg.Retrievers = append(cfg.Retrievers, NamedRetriever{Name: string(rune('r' + i)), Retriever: r, Family: r.family})
	}
	for i, p := range providers {
		cfg.Providers = append(cfg.Providers, NamedProvider{Name: string(rune('a' + i)), Provider: p})
	}
	in := newInstance(cfg, Options{Logger: discardLogger(), Clock: clock, AttemptTimeout: DefaultAttemptTimeout, Version: "1.2.3"})
	in.jitter = func(int64) int64 { return 0 }
	return in, rec
}

// cycle runs one cycle the way run does: the tick, then the hooks.
func cycle(in *instance) {
	in.runHooks(context.Background(), in.tick(context.Background()))
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestOnlyHooksWithEventsGetThem(t *testing.T) {
	plain := &fakeHook{}
	rec := &eventRecorder{}
	in := newInstance(Instance{
		Name:       "test",
		Interval:   testInterval,
		Retrievers: []NamedRetriever{{Name: "r", Retriever: newFakeRetriever("203.0.113.1")}},
		Providers:  []NamedProvider{{Name: "p", Provider: newFakeProvider()}},
		Hooks:      []Hook{plain, rec},
	}, Options{Logger: discardLogger(), Clock: newFakeClock(), AttemptTimeout: DefaultAttemptTimeout})

	cycle(in)

	if got := plain.all(); len(got) != 1 {
		t.Errorf("plain hook got %d cycles, want 1: it still has AfterCycle", len(got))
	}
	if len(rec.cycles) != 1 || len(rec.of(KindCycle)) != 1 {
		t.Errorf("recorder got %d cycles and %v events, want 1 cycle and a cycle event", len(rec.cycles), rec.kinds())
	}
}

func TestInstanceStatusReportsOnlyTransitions(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	in, rec := newEventInstance(clock, []*fakeRetriever{retriever}, newFakeProvider())

	cycle(in) // first success: not news
	if got := rec.of(KindInstanceStatus); len(got) != 0 {
		t.Fatalf("status events after a first success = %+v, want none", got)
	}

	retriever.set("203.0.113.1", errBoom)
	cycle(in) // ok -> failed
	cycle(in) // still failed

	got := rec.of(KindInstanceStatus)
	if len(got) != 1 || !got[0].Failed || !errors.Is(got[0].Err, errBoom) {
		t.Fatalf("status events = %+v, want one failure carrying the error", got)
	}
	if got[0].Instance != "test" || !got[0].Time.Equal(epoch) {
		t.Errorf("event = %+v, want the instance and the clock's time", got[0])
	}

	retriever.set("203.0.113.1", nil)
	cycle(in) // failed -> ok

	got = rec.of(KindInstanceStatus)
	if len(got) != 2 || got[1].Failed || got[1].Err != nil {
		t.Errorf("status events = %+v, want a failure then a recovery", got)
	}
}

func TestInstanceStatusFirstObservationIsAFailure(t *testing.T) {
	retriever := newFakeRetriever("203.0.113.1")
	retriever.set("203.0.113.1", errBoom)
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{retriever}, newFakeProvider())

	cycle(in)

	if got := rec.of(KindInstanceStatus); len(got) != 1 || !got[0].Failed {
		t.Errorf("status events = %+v, want the first failure", got)
	}
}

func TestProviderStatusTransitions(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	failing := true
	provider.fail = func(int) error {
		if failing {
			return errBoom
		}
		return nil
	}
	in, rec := newEventInstance(clock, []*fakeRetriever{retriever}, provider)

	cycle(in) // write fails: first observation, a failure
	cycle(in) // backing off: skipped, no change of state

	got := rec.of(KindProviderStatus)
	if len(got) != 1 || !got[0].Failed || got[0].Name != "a" || !errors.Is(got[0].Err, errBoom) {
		t.Fatalf("provider events = %+v, want one failure of provider a", got)
	}
	if provider.count() != 1 {
		t.Fatalf("provider written %d times, want 1: the second cycle backs off", provider.count())
	}

	failing = false
	clock.Advance(testInterval) // past the backoff
	cycle(in)                   // written, recovered

	got = rec.of(KindProviderStatus)
	if len(got) != 2 || got[1].Failed {
		t.Fatalf("provider events = %+v, want a failure then a recovery", got)
	}

	cycle(in) // address unchanged: skipped
	if got = rec.of(KindProviderStatus); len(got) != 2 {
		t.Errorf("provider events = %+v, want no more: an unchanged address is not an attempt", got)
	}
}

func TestProviderFirstSuccessIsNotAnEvent(t *testing.T) {
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{newFakeRetriever("203.0.113.1")}, newFakeProvider())

	cycle(in)

	if got := rec.of(KindProviderStatus); len(got) != 0 {
		t.Errorf("provider events = %+v, want none", got)
	}
}

func TestProvidersAreTrackedSeparately(t *testing.T) {
	good, bad := newFakeProvider(), newFakeProvider()
	bad.fail = func(int) error { return errBoom }
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{newFakeRetriever("203.0.113.1")}, good, bad)

	cycle(in)

	got := rec.of(KindProviderStatus)
	if len(got) != 1 || got[0].Name != "b" {
		t.Errorf("provider events = %+v, want only the failure of b", got)
	}
}

func TestRetrieverStatusTransitions(t *testing.T) {
	primary := newFakeRetriever("203.0.113.1")
	primary.set("203.0.113.1", errBoom)
	fallback := newFakeRetriever("203.0.113.2")
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{primary, fallback}, newFakeProvider())

	cycle(in) // primary fails, fallback serves
	cycle(in) // same again: no new event

	got := rec.of(KindRetrieverStatus)
	if len(got) != 1 || !got[0].Failed || got[0].Name != "r" || !errors.Is(got[0].Err, errBoom) {
		t.Fatalf("retriever events = %+v, want one failure of r", got)
	}
	// The failed retriever is part of the cycle's error even though the
	// fallback supplied the address, so the instance is reported as failed too.
	if status := rec.of(KindInstanceStatus); len(status) != 1 || !status[0].Failed {
		t.Errorf("status events = %+v, want the one failure of the cycle", status)
	}

	primary.set("203.0.113.1", nil)
	cycle(in)

	got = rec.of(KindRetrieverStatus)
	if len(got) != 2 || got[1].Failed {
		t.Errorf("retriever events = %+v, want a failure then a recovery", got)
	}
}

// A retriever that is not called, because an earlier one filled the family,
// keeps its state: it is neither recovered nor failed by being left alone.
func TestRetrieverNotCalledKeepsItsState(t *testing.T) {
	primary := newFakeRetriever("203.0.113.1").withFamily(familyIPv4)
	primary.set("203.0.113.1", errBoom)
	fallback := newFakeRetriever("203.0.113.2").withFamily(familyIPv4)
	fallback.set("203.0.113.2", errBoom)
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{primary, fallback}, newFakeProvider())

	cycle(in) // both fail

	primary.set("203.0.113.1", nil)
	cycle(in) // primary serves, fallback is not called

	if fallback.callCount() != 1 {
		t.Fatalf("fallback called %d times, want 1", fallback.callCount())
	}

	got := rec.of(KindRetrieverStatus)
	if len(got) != 3 {
		t.Fatalf("retriever events = %+v, want failures of both and the recovery of the primary", got)
	}
	if got[2].Name != "r" || got[2].Failed {
		t.Errorf("last event = %+v, want the recovery of r, and none for s", got[2])
	}
}

func TestRetrieverReturningNoAddressIsAFailure(t *testing.T) {
	retriever := newFakeRetriever("203.0.113.1")
	retriever.addrs = plugin.Addresses{}
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{retriever}, newFakeProvider())

	cycle(in)

	if got := rec.of(KindRetrieverStatus); len(got) != 1 || !got[0].Failed {
		t.Errorf("retriever events = %+v, want a failure", got)
	}
}

func TestIPChangeWithUnknownOldAddress(t *testing.T) {
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{newFakeRetriever("203.0.113.1")}, newFakeProvider())

	cycle(in)

	got := rec.of(KindIPChange)
	if len(got) != 1 {
		t.Fatalf("ip_change events = %+v, want 1", got)
	}

	want := []AddressChange{{Provider: "a", New: addr("203.0.113.1")}}
	if !slices.Equal(got[0].Changes, want) {
		t.Errorf("changes = %+v, want %+v: the old address is not known yet", got[0].Changes, want)
	}
}

func TestIPChangeWithKnownOldAddress(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	in, rec := newEventInstance(clock, []*fakeRetriever{retriever}, newFakeProvider())

	cycle(in)
	cycle(in) // unchanged: no event
	retriever.set("203.0.113.2", nil)
	cycle(in)

	got := rec.of(KindIPChange)
	if len(got) != 2 {
		t.Fatalf("ip_change events = %+v, want 2 (first write, then the change)", got)
	}

	want := []AddressChange{{Provider: "a", Old: addr("203.0.113.1"), New: addr("203.0.113.2")}}
	if !slices.Equal(got[1].Changes, want) {
		t.Errorf("changes = %+v, want %+v", got[1].Changes, want)
	}
}

// After a failed write the record is unknown, so the old address is too.
func TestIPChangeAfterAFailedWriteHasNoOldAddress(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	provider.fail = func(call int) error {
		if call == 2 {
			return errBoom
		}
		return nil
	}
	in, rec := newEventInstance(clock, []*fakeRetriever{retriever}, provider)

	cycle(in) // written
	retriever.set("203.0.113.2", nil)
	clock.Advance(testInterval)
	cycle(in) // fails
	clock.Advance(testInterval)
	cycle(in) // written again

	got := rec.of(KindIPChange)
	if len(got) != 2 {
		t.Fatalf("ip_change events = %+v, want 2: a failed write is not a change", got)
	}

	want := []AddressChange{{Provider: "a", New: addr("203.0.113.2")}}
	if !slices.Equal(got[1].Changes, want) {
		t.Errorf("changes = %+v, want %+v", got[1].Changes, want)
	}
}

// One event per cycle, however many providers and families it touches.
func TestIPChangeIsOneEventPerCycle(t *testing.T) {
	retriever := newFakeRetriever("203.0.113.1")
	retriever.setDual("203.0.113.1", "2001:db8::1")
	retriever.family = familyDual
	first, second := newFakeProvider(), newFakeProvider()
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{retriever}, first, second)

	cycle(in)

	got := rec.of(KindIPChange)
	if len(got) != 1 {
		t.Fatalf("ip_change events = %+v, want exactly 1", got)
	}

	want := []AddressChange{
		{Provider: "a", New: addr("203.0.113.1")},
		{Provider: "a", IPv6: true, New: addr("2001:db8::1")},
		{Provider: "b", New: addr("203.0.113.1")},
		{Provider: "b", IPv6: true, New: addr("2001:db8::1")},
	}
	if !slices.Equal(got[0].Changes, want) {
		t.Errorf("changes = %+v, want %+v", got[0].Changes, want)
	}
}

func TestIPChangeOnlyListsSuccessfulWrites(t *testing.T) {
	good, bad := newFakeProvider(), newFakeProvider()
	bad.fail = func(int) error { return errBoom }
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{newFakeRetriever("203.0.113.1")}, good, bad)

	cycle(in)

	got := rec.of(KindIPChange)
	if len(got) != 1 || len(got[0].Changes) != 1 || got[0].Changes[0].Provider != "a" {
		t.Errorf("ip_change events = %+v, want one change, of the provider that worked", got)
	}
}

func TestNoIPChangeWhenNothingWasWritten(t *testing.T) {
	provider := newFakeProvider()
	provider.fail = func(int) error { return errBoom }
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{newFakeRetriever("203.0.113.1")}, provider)

	cycle(in)

	if got := rec.of(KindIPChange); len(got) != 0 {
		t.Errorf("ip_change events = %+v, want none", got)
	}
}

func TestCycleEventForEveryCycle(t *testing.T) {
	retriever := newFakeRetriever("203.0.113.1")
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{retriever}, newFakeProvider())

	cycle(in)
	cycle(in) // nothing to write, still a cycle
	retriever.set("203.0.113.1", errBoom)
	cycle(in)

	got := rec.of(KindCycle)
	if len(got) != 3 {
		t.Fatalf("cycle events = %+v, want 3", got)
	}
	if got[0].Failed || got[1].Failed || !got[2].Failed || !errors.Is(got[2].Err, errBoom) {
		t.Errorf("cycle events = %+v, want two successes and a failure", got)
	}
}

func TestInterruptedCycleEmitsNothing(t *testing.T) {
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{newFakeRetriever("203.0.113.1")}, newFakeProvider())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in.runHooks(ctx, in.tick(ctx))

	if got := rec.all(); len(got) != 0 {
		t.Errorf("events = %+v, want none: shutdown is not a result", got)
	}
}

// Order within a cycle: what happened on the way, then the verdict.
func TestEventOrderWithinACycle(t *testing.T) {
	retriever := newFakeRetriever("203.0.113.1")
	retriever.set("203.0.113.1", errBoom)
	provider := newFakeProvider()
	in, rec := newEventInstance(newFakeClock(), []*fakeRetriever{retriever}, provider)

	cycle(in)

	want := []EventKind{KindRetrieverStatus, KindInstanceStatus, KindCycle}
	if got := rec.kinds(); !slices.Equal(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

func TestLifecycleStartedBeforeTheFirstCycleAndStoppedAfterTheLast(t *testing.T) {
	rec := &eventRecorder{}
	clock := newFakeClock()
	provider := newFakeProvider()
	cfg := Instance{
		Name:       "lab",
		Interval:   testInterval,
		Retrievers: []NamedRetriever{{Name: "r", Retriever: newFakeRetriever("203.0.113.1")}},
		Providers:  []NamedProvider{{Name: "p", Provider: provider}},
		Hooks:      []Hook{rec},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- Run(ctx, []Instance{cfg}, Options{Logger: discardLogger(), Clock: clock, Version: "1.2.3"})
	}()

	receive(t, provider.called)
	clock.settle(t)
	cancel()

	if err := receive(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := rec.all()
	if len(events) < 2 {
		t.Fatalf("events = %+v, want at least started and stopped", events)
	}

	started, stopped := events[0], events[len(events)-1]
	if started.Kind != KindStarted || started.Instance != "lab" || started.Version != "1.2.3" {
		t.Errorf("first event = %+v, want started for lab with the version", started)
	}
	if stopped.Kind != KindStopped || stopped.Instance != "lab" || stopped.Version != "1.2.3" {
		t.Errorf("last event = %+v, want stopped for lab with the version", stopped)
	}

	if rec.cancelled[0] {
		t.Error("started came with a cancelled context")
	}
	if rec.cancelled[len(events)-1] {
		t.Error("stopped came with a cancelled context: it could not be published")
	}

	for _, ev := range events[1 : len(events)-1] {
		if ev.Kind == KindStarted || ev.Kind == KindStopped {
			t.Errorf("event %+v between started and stopped", ev)
		}
	}
}

// Every instance reports its own start and stop.
func TestLifecycleIsPerInstance(t *testing.T) {
	rec := &eventRecorder{}
	clock := newFakeClock()
	first, second := newFakeProvider(), newFakeProvider()

	one := testInstance("one", testInterval, newFakeRetriever("203.0.113.1"), first)
	one.Hooks = []Hook{rec}
	two := testInstance("two", testInterval, newFakeRetriever("203.0.113.2"), second)
	two.Hooks = []Hook{rec}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- Run(ctx, []Instance{one, two}, Options{Logger: discardLogger(), Clock: clock}) }()

	receive(t, first.called)
	receive(t, second.called)
	clock.settle(t)
	cancel()

	if err := receive(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, kind := range []EventKind{KindStarted, KindStopped} {
		names := map[string]bool{}
		for _, ev := range rec.of(kind) {
			names[ev.Instance] = true
		}

		if len(names) != 2 || !names["one"] || !names["two"] {
			t.Errorf("kind %v seen for %v, want one and two", kind, names)
		}
	}
}

func TestTrackedState(t *testing.T) {
	var s trackedState

	steps := []struct {
		failed bool
		want   bool
	}{
		{false, false}, // first success: nothing
		{false, false},
		{true, true}, // failure
		{true, false},
		{false, true}, // recovery
	}

	for i, step := range steps {
		if got := s.observe(step.failed); got != step.want {
			t.Errorf("step %d: observe(%v) = %v, want %v", i, step.failed, got, step.want)
		}
	}

	var fresh trackedState
	if !fresh.observe(true) {
		t.Error("a first failure must be reported")
	}
}
