package runner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

const testInterval = time.Minute

var errBoom = errors.New("boom")

func TestTickWritesToAllProvidersWhenOneFails(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	first, second, third := newFakeProvider(), newFakeProvider(), newFakeProvider()
	first.fail = func(int) error { return errBoom }
	in := newTestInstance(clock, testInterval, retriever, first, second, third)

	err := in.tick(context.Background())

	if !errors.Is(err, errBoom) {
		t.Fatalf("tick error = %v, want it to wrap %v", err, errBoom)
	}
	for name, p := range map[string]*fakeProvider{"first": first, "second": second, "third": third} {
		if got := p.count(); got != 1 {
			t.Errorf("%s provider called %d times, want 1", name, got)
		}
	}
}

func TestTickJoinsAllProviderErrors(t *testing.T) {
	errOne, errTwo := errors.New("one"), errors.New("two")
	first, second := newFakeProvider(), newFakeProvider()
	first.fail = func(int) error { return errOne }
	second.fail = func(int) error { return errTwo }
	in := newTestInstance(newFakeClock(), testInterval, newFakeRetriever("203.0.113.1"), first, second)

	err := in.tick(context.Background())

	if !errors.Is(err, errOne) || !errors.Is(err, errTwo) {
		t.Errorf("tick error = %v, want both provider errors", err)
	}
}

func TestTickRetriesOnlyTheFailedProvider(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	flaky, healthy := newFakeProvider(), newFakeProvider()
	flaky.fail = func(call int) error {
		if call == 1 {
			return errBoom
		}
		return nil
	}
	in := newTestInstance(clock, testInterval, retriever, flaky, healthy)

	_ = in.tick(context.Background())
	clock.Advance(testInterval)
	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	if got := flaky.count(); got != 2 {
		t.Errorf("failed provider called %d times, want 2", got)
	}
	if got := healthy.count(); got != 1 {
		t.Errorf("successful provider called %d times, want 1", got)
	}
}

func TestTickSkipsProvidersWhenAddressUnchanged(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	first, second := newFakeProvider(), newFakeProvider()
	in := newTestInstance(clock, testInterval, retriever, first, second)

	for range 3 {
		if err := in.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Advance(testInterval)
	}

	if first.count() != 1 || second.count() != 1 {
		t.Errorf("writes = %d and %d, want 1 each", first.count(), second.count())
	}
}

func TestTickWritesAgainWhenAddressChanges(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	in := newTestInstance(clock, testInterval, retriever, provider)

	_ = in.tick(context.Background())
	retriever.set("203.0.113.2", nil)
	clock.Advance(testInterval)
	_ = in.tick(context.Background())

	want := []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("203.0.113.2")}
	if !slices.Equal(provider.writes, want) {
		t.Errorf("writes = %v, want %v", provider.writes, want)
	}
}

func TestTickRewritesAfterFailedWriteEvenIfAddressReverts(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	provider.fail = func(call int) error {
		if call == 2 {
			return errBoom
		}
		return nil
	}
	in := newTestInstance(clock, testInterval, retriever, provider)

	_ = in.tick(context.Background()) // writes .1
	retriever.set("203.0.113.2", nil)
	clock.Advance(testInterval)
	_ = in.tick(context.Background()) // fails writing .2, record content is unknown
	retriever.set("203.0.113.1", nil)
	clock.Advance(testInterval)
	_ = in.tick(context.Background())

	if got := provider.count(); got != 3 {
		t.Errorf("provider called %d times, want 3: the reverted address must be written again", got)
	}
}

func TestBackoffSkipsTicksAndGapsGrow(t *testing.T) {
	clock := newFakeClock()
	provider := newFakeProvider()
	provider.fail = func(int) error { return errBoom }
	in := newTestInstance(clock, testInterval, newFakeRetriever("203.0.113.1"), provider)

	var attempts []int
	for tick := range 40 {
		before := provider.count()
		_ = in.tick(context.Background())
		if provider.count() > before {
			attempts = append(attempts, tick)
		}
		clock.Advance(testInterval)
	}

	// With the lowest jitter the delays are 30s, 1m, 2m, 4m, 8m, then the
	// 30m ceiling halved to 15m.
	want := []int{0, 1, 2, 4, 8, 16, 31}
	if !slices.Equal(attempts, want) {
		t.Errorf("attempts at ticks %v, want %v", attempts, want)
	}
}

func TestSuccessResetsBackoff(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	provider.fail = func(call int) error {
		if call <= 3 {
			return errBoom
		}
		return nil
	}
	in := newTestInstance(clock, testInterval, retriever, provider)
	state := in.providers[0]

	for range 4 {
		_ = in.tick(context.Background())
		clock.Advance(time.Hour)
	}
	if state.failures != 0 || !state.next.IsZero() {
		t.Fatalf("after success failures=%d next=%v, want a clean state", state.failures, state.next)
	}

	retriever.set("203.0.113.2", nil)
	provider.fail = func(int) error { return errBoom }
	_ = in.tick(context.Background())
	if state.failures != 1 {
		t.Errorf("failures = %d, want the count to restart at 1", state.failures)
	}
}

func TestJitterSeparatesInstances(t *testing.T) {
	schedule := func() []time.Duration {
		clock := newFakeClock()
		provider := newFakeProvider()
		provider.fail = func(int) error { return errBoom }
		// Built the way Run builds it, with the production jitter source.
		in := newInstance(Instance{
			Name:      "test",
			Interval:  testInterval,
			Retriever: newFakeRetriever("203.0.113.1"),
			Providers: []NamedProvider{{Name: "p", Provider: provider}},
		}, Options{Logger: discardLogger(), Clock: clock, AttemptTimeout: DefaultAttemptTimeout})

		var retryIn []time.Duration
		for range 6 {
			_ = in.tick(context.Background())
			retryIn = append(retryIn, in.providers[0].next.Sub(clock.Now()))
			clock.Advance(time.Hour)
		}
		return retryIn
	}

	if a, b := schedule(), schedule(); slices.Equal(a, b) {
		t.Errorf("two instances with the same schedule retried identically: %v", a)
	}
}

func TestRetrieverErrorSkipsProvidersAndRecovers(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	retriever.set("203.0.113.1", errBoom)
	provider := newFakeProvider()
	in := newTestInstance(clock, testInterval, retriever, provider)

	err := in.tick(context.Background())
	if !errors.Is(err, errBoom) {
		t.Errorf("tick error = %v, want it to wrap %v", err, errBoom)
	}
	if provider.count() != 0 {
		t.Fatal("provider must not be called when the retriever fails")
	}

	retriever.set("203.0.113.1", nil)
	clock.Advance(testInterval)
	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick after recovery: %v", err)
	}
	if provider.count() != 1 {
		t.Errorf("provider called %d times after recovery, want 1", provider.count())
	}
}

func TestRetrieverInvalidAddressIsAnError(t *testing.T) {
	provider := newFakeProvider()
	retriever := newFakeRetriever("203.0.113.1")
	retriever.addr = netip.Addr{}
	in := newTestInstance(newFakeClock(), testInterval, retriever, provider)

	if err := in.tick(context.Background()); err == nil {
		t.Error("tick succeeded with an invalid address")
	}
	if provider.count() != 0 {
		t.Error("an invalid address reached a provider")
	}
}

func TestAttemptTimeoutAbortsStuckProvider(t *testing.T) {
	clock := newFakeClock()
	provider := newFakeProvider()
	provider.block = true
	in := newTestInstance(clock, testInterval, newFakeRetriever("203.0.113.1"), provider)

	done := make(chan error, 1)
	go func() { done <- in.tick(context.Background()) }()
	receive(t, provider.called)
	clock.Advance(DefaultAttemptTimeout)

	err := receive(t, done)
	if !errors.Is(err, errAttemptTimeout) {
		t.Errorf("tick error = %v, want it to wrap %v", err, errAttemptTimeout)
	}
	if in.providers[0].failures != 1 {
		t.Errorf("failures = %d, want 1: a timeout is a failure", in.providers[0].failures)
	}
}

func TestShutdownDuringWriteIsNotAFailure(t *testing.T) {
	provider := newFakeProvider()
	provider.block = true
	in := newTestInstance(newFakeClock(), testInterval, newFakeRetriever("203.0.113.1"), provider)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- in.tick(ctx) }()
	receive(t, provider.called)
	cancel()

	if err := receive(t, done); err != nil {
		t.Errorf("tick error = %v, want nil on shutdown", err)
	}
	if state := in.providers[0]; state.failures != 0 || !state.next.IsZero() {
		t.Errorf("state after shutdown: failures=%d next=%v, want it untouched", state.failures, state.next)
	}
}

// advisedRetriever is a fakeRetriever whose service asks for a minimum interval.
type advisedRetriever struct {
	*fakeRetriever
	recommended time.Duration
}

func (r advisedRetriever) RecommendedInterval() time.Duration { return r.recommended }

func TestWarnShortInterval(t *testing.T) {
	tests := []struct {
		name      string
		retriever plugin.Retriever
		interval  time.Duration
		wantWarn  bool
	}{
		{"shorter than recommended", advisedRetriever{newFakeRetriever("203.0.113.1"), time.Minute}, 10 * time.Second, true},
		{"equal to recommended", advisedRetriever{newFakeRetriever("203.0.113.1"), time.Minute}, time.Minute, false},
		{"longer than recommended", advisedRetriever{newFakeRetriever("203.0.113.1"), time.Minute}, 5 * time.Minute, false},
		{"retriever gives no advice", newFakeRetriever("203.0.113.1"), time.Second, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			in := newInstance(Instance{
				Name:      "homelab",
				Interval:  tt.interval,
				Retriever: tt.retriever,
				Providers: []NamedProvider{{Name: "a", Provider: newFakeProvider()}},
			}, Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), Clock: newFakeClock(), AttemptTimeout: DefaultAttemptTimeout})

			in.warnShortInterval()

			out := buf.String()
			if got := strings.Contains(out, "level=WARN"); got != tt.wantWarn {
				t.Errorf("warned = %t, want %t; log %q", got, tt.wantWarn, out)
			}
			if tt.wantWarn && !strings.Contains(out, "instance=homelab") {
				t.Errorf("log %q does not name the instance", out)
			}
		})
	}
}

func TestLogsIdentifyInstanceAndProvider(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	provider := newFakeProvider()
	provider.fail = func(int) error { return errBoom }
	in := newInstance(Instance{
		Name:      "homelab",
		Interval:  testInterval,
		Retriever: newFakeRetriever("203.0.113.1"),
		Providers: []NamedProvider{{Name: "selectel", Provider: provider}},
	}, Options{Logger: log, Clock: newFakeClock(), AttemptTimeout: DefaultAttemptTimeout})

	_ = in.tick(context.Background())

	out := buf.String()
	for _, want := range []string{"instance=homelab", "provider=selectel", "update failed", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not contain %q", out, want)
		}
	}
}
