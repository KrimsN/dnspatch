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

func TestTickWithTwoRetrieversSendsBothFamiliesInOneCall(t *testing.T) {
	clock := newFakeClock()
	v4, v6 := newFakeRetriever("203.0.113.1"), newFakeRetriever("2001:db8::1")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{v4, v6}, provider)

	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := provider.count(); got != 1 {
		t.Fatalf("provider called %d times, want 1", got)
	}
	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.1"), V6: netip.MustParseAddr("2001:db8::1")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want %+v", got, want)
	}
}

func TestTickWithTwoRetrieversSendsOnlyTheChangedFamily(t *testing.T) {
	clock := newFakeClock()
	v4, v6 := newFakeRetriever("203.0.113.1"), newFakeRetriever("2001:db8::1")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{v4, v6}, provider)

	_ = in.tick(context.Background())
	v6.set("2001:db8::2", nil)
	clock.Advance(testInterval)
	_ = in.tick(context.Background())

	if got := provider.count(); got != 2 {
		t.Fatalf("provider called %d times, want 2", got)
	}
	want := plugin.Addresses{V6: netip.MustParseAddr("2001:db8::2")}
	if got := provider.updates[1]; got != want {
		t.Errorf("second call addrs = %+v, want %+v (v4 unchanged, must be left out)", got, want)
	}
}

func TestTickWithOneRetrieverFailingStillUpdatesTheOtherFamily(t *testing.T) {
	clock := newFakeClock()
	v4, v6 := newFakeRetriever("203.0.113.1"), newFakeRetriever("2001:db8::1")
	v6.set("2001:db8::1", errBoom)
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{v4, v6}, provider)

	err := in.tick(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("tick error = %v, want it to wrap %v", err, errBoom)
	}

	if got := provider.count(); got != 1 {
		t.Fatalf("provider called %d times, want 1", got)
	}
	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.1")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want only v4: %+v", got, want)
	}
}

func TestTickWithRedundantRetrieverOfTheSameFamilyKeepsTheFirst(t *testing.T) {
	clock := newFakeClock()
	v4a, v4b := newFakeRetriever("203.0.113.1"), newFakeRetriever("203.0.113.2")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{v4a, v4b}, provider)

	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Neither retriever ever fills V6, so both are called every tick (the
	// loop only stops early once every family is filled); v4b's redundant
	// address is a fallback source for v4a, not an error, and is ignored.
	if got := provider.count(); got != 1 {
		t.Fatalf("provider called %d times, want 1", got)
	}
	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.1")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want the first retriever's address: %+v", got, want)
	}
}

func TestTickFallsBackToTheNextRetrieverOnFailure(t *testing.T) {
	clock := newFakeClock()
	failing := newFakeRetriever("203.0.113.1")
	failing.set("203.0.113.1", errBoom)
	fallback := newFakeRetriever("203.0.113.9")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{failing, fallback}, provider)

	err := in.tick(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("tick error = %v, want it to wrap %v", err, errBoom)
	}

	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.9")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want the fallback retriever's address: %+v", got, want)
	}
}

func TestTickStopsCallingRetrieversOnceEveryFamilyIsFilled(t *testing.T) {
	clock := newFakeClock()
	v4, v6 := newFakeRetriever("203.0.113.1"), newFakeRetriever("2001:db8::1")
	unused := newFakeRetriever("203.0.113.9")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{v4, v6, unused}, provider)

	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := unused.callCount(); got != 0 {
		t.Errorf("third retriever called %d times, want 0: both families were already filled", got)
	}
}

func TestTickWithADualFamilyRetrieverFillsBothInOneCall(t *testing.T) {
	clock := newFakeClock()
	dual := newFakeRetriever("203.0.113.1")
	dual.setDual("203.0.113.1", "2001:db8::1")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{dual}, provider)

	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.1"), V6: netip.MustParseAddr("2001:db8::1")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want %+v", got, want)
	}
}

func TestTickNeverPollsForAFamilyNoRetrieverDeclares(t *testing.T) {
	clock := newFakeClock()
	primary := newFakeRetriever("203.0.113.1").withFamily("ipv4")
	fallback1 := newFakeRetriever("203.0.113.2").withFamily("ipv4")
	fallback2 := newFakeRetriever("203.0.113.3").withFamily("ipv4")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{primary, fallback1, fallback2}, provider)

	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// No retriever ever declares ipv6, so the instance never looks for it:
	// once the first retriever fills V4, the rest are not even called.
	if got := fallback1.callCount(); got != 0 {
		t.Errorf("fallback1 called %d times, want 0: ipv6 was never needed and ipv4 was already filled", got)
	}
	if got := fallback2.callCount(); got != 0 {
		t.Errorf("fallback2 called %d times, want 0: ipv6 was never needed and ipv4 was already filled", got)
	}
	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.1")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want %+v", got, want)
	}
}

func TestTickSkipsAFamilyPinnedRetrieverOnceItsFamilyIsFilled(t *testing.T) {
	clock := newFakeClock()
	dual := newFakeRetriever("203.0.113.1").withFamily("dual")
	dual.setDual("203.0.113.1", "2001:db8::1")
	v6Fallback := newFakeRetriever("2001:db8::2").withFamily("ipv6")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{dual, v6Fallback}, provider)

	if err := in.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// The dual retriever already filled both families in one call, so the
	// ipv6-pinned fallback, which could only ever help ipv6, is never called.
	if got := v6Fallback.callCount(); got != 0 {
		t.Errorf("v6 fallback called %d times, want 0: ipv6 was already filled by the dual retriever", got)
	}
}

func TestTickUsesFamilyHintsToBuildPerFamilyFallback(t *testing.T) {
	// Mirrors a config where each retriever declares its own family:
	// [[instance.retriever]] ref = "icanhazip" family = "dual"
	// [[instance.retriever]] ref = "ipify"     family = "ipv6"
	// [[instance.retriever]] ref = "2ip"       family = "ipv4"
	clock := newFakeClock()
	dual := newFakeRetriever("203.0.113.1").withFamily("dual")
	dual.set("203.0.113.1", errBoom) // the dual source is down this tick
	ipv6Only := newFakeRetriever("2001:db8::1").withFamily("ipv6")
	ipv4Only := newFakeRetriever("203.0.113.9").withFamily("ipv4")
	provider := newFakeProvider()
	in := newTestInstanceMulti(clock, testInterval, []*fakeRetriever{dual, ipv6Only, ipv4Only}, provider)

	err := in.tick(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("tick error = %v, want it to wrap %v", err, errBoom)
	}

	// Both single-family fallbacks are needed and get called, since the dual
	// source failed to provide either family.
	want := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.9"), V6: netip.MustParseAddr("2001:db8::1")}
	if got := provider.updates[0]; got != want {
		t.Errorf("addrs sent = %+v, want %+v", got, want)
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
			Name:       "test",
			Interval:   testInterval,
			Retrievers: []NamedRetriever{{Name: "r", Retriever: newFakeRetriever("203.0.113.1")}},
			Providers:  []NamedProvider{{Name: "p", Provider: provider}},
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
	retriever.addrs = plugin.Addresses{}
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
				Name:       "homelab",
				Interval:   tt.interval,
				Retrievers: []NamedRetriever{{Name: "r", Retriever: tt.retriever}},
				Providers:  []NamedProvider{{Name: "a", Provider: newFakeProvider()}},
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
		Name:       "homelab",
		Interval:   testInterval,
		Retrievers: []NamedRetriever{{Name: "r", Retriever: newFakeRetriever("203.0.113.1")}},
		Providers:  []NamedProvider{{Name: "example", Provider: provider}},
	}, Options{Logger: log, Clock: newFakeClock(), AttemptTimeout: DefaultAttemptTimeout})

	_ = in.tick(context.Background())

	out := buf.String()
	for _, want := range []string{"instance=homelab", "provider=example", "update failed", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not contain %q", out, want)
		}
	}
}
