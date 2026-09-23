package runner

import (
	"context"
	"net/netip"
	"runtime"
	"strings"
	"testing"
	"time"
)

// startRun runs the instances in the background and returns a stop function
// that cancels them and waits for Run to return.
func startRun(t *testing.T, clock Clock, instances ...Instance) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	t.Cleanup(cancel)
	go func() {
		done <- Run(ctx, instances, Options{Logger: discardLogger(), Clock: clock})
	}()
	return func() {
		t.Helper()
		cancel()
		if err := receive(t, done); err != nil {
			t.Errorf("Run: %v", err)
		}
	}
}

func testInstance(name string, interval time.Duration, r *fakeRetriever, p *fakeProvider) Instance {
	return Instance{
		Name:       name,
		Interval:   interval,
		Retrievers: []NamedRetriever{{Name: "r", Retriever: r}},
		Providers:  []NamedProvider{{Name: "p", Provider: p}},
	}
}

func TestRunFirstTickIsImmediate(t *testing.T) {
	clock := newFakeClock() // never advanced: any write comes from the immediate tick
	provider := newFakeProvider()
	stop := startRun(t, clock, testInstance("a", time.Hour, newFakeRetriever("203.0.113.1"), provider))
	defer stop()

	if got := receive(t, provider.called); got != netip.MustParseAddr("203.0.113.1") {
		t.Errorf("first write = %v", got)
	}
}

func TestRunTicksOnInterval(t *testing.T) {
	clock := newFakeClock()
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	stop := startRun(t, clock, testInstance("a", testInterval, retriever, provider))
	defer stop()

	receive(t, provider.called)
	retriever.set("203.0.113.2", nil)
	clock.settle(t)
	clock.Advance(testInterval)

	if got := receive(t, provider.called); got != netip.MustParseAddr("203.0.113.2") {
		t.Errorf("second write = %v, want the new address", got)
	}
}

func TestRunInstancesAreIndependent(t *testing.T) {
	clock := newFakeClock()
	fastRetriever, slowRetriever := newFakeRetriever("203.0.113.1"), newFakeRetriever("198.51.100.1")
	fast, slow := newFakeProvider(), newFakeProvider()
	stop := startRun(t, clock,
		testInstance("fast", time.Minute, fastRetriever, fast),
		testInstance("slow", time.Hour, slowRetriever, slow),
	)
	defer stop()

	receive(t, fast.called)
	receive(t, slow.called)
	fastRetriever.set("203.0.113.2", nil)
	slowRetriever.set("198.51.100.2", nil)
	clock.settle(t)
	clock.Advance(time.Minute)

	receive(t, fast.called)
	if slow.count() != 1 {
		t.Errorf("slow instance wrote %d times after a minute, want 1", slow.count())
	}
}

func TestRunReturnsOnCancelWithoutLeakingGoroutines(t *testing.T) {
	clock := newFakeClock()
	provider := newFakeProvider()
	stop := startRun(t, clock,
		testInstance("a", time.Minute, newFakeRetriever("203.0.113.1"), provider),
		testInstance("b", time.Minute, newFakeRetriever("203.0.113.1"), newFakeProvider()),
	)
	receive(t, provider.called)
	if got := runnerGoroutines(); got == 0 {
		t.Fatal("no instance goroutine found while running: the leak check is blind")
	}

	stop()

	deadline := time.Now().Add(5 * time.Second)
	for runnerGoroutines() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runnerGoroutines(); got > 0 {
		t.Errorf("%d instance goroutines still alive after Run returned", got)
	}
}

// runnerGoroutines counts goroutines currently executing instance code.
func runnerGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "runner.(*instance).run")
}

func TestRunWithDefaultsUsesTheSystemClock(t *testing.T) {
	provider := newFakeProvider()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []Instance{testInstance("a", time.Millisecond, newFakeRetriever("203.0.113.1"), provider)}, Options{})
	}()

	receive(t, provider.called)
	cancel()

	if err := receive(t, done); err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestRunDoesNothingWhenContextIsAlreadyCancelled(t *testing.T) {
	retriever := newFakeRetriever("203.0.113.1")
	provider := newFakeProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, []Instance{testInstance("a", time.Minute, retriever, provider)}, Options{Logger: discardLogger(), Clock: newFakeClock()})

	if err != nil {
		t.Errorf("Run: %v", err)
	}
	if provider.count() != 0 {
		t.Error("provider was called although the context was cancelled before the start")
	}
}

func TestRunStopsAStuckWriteOnCancel(t *testing.T) {
	provider := newFakeProvider()
	provider.block = true
	stop := startRun(t, newFakeClock(), testInstance("a", time.Minute, newFakeRetriever("203.0.113.1"), provider))

	receive(t, provider.called)
	stop() // fails the test if Run does not return
}

func TestRunRejectsInvalidInstances(t *testing.T) {
	good := testInstance("good", time.Minute, newFakeRetriever("203.0.113.1"), newFakeProvider())
	noInterval := good
	noInterval.Interval = 0
	noRetriever := good
	noRetriever.Retrievers = nil
	noProviders := good
	noProviders.Providers = nil
	nilProvider := good
	nilProvider.Providers = []NamedProvider{{Name: "x"}}

	tests := map[string]struct {
		instances []Instance
		want      string
	}{
		"none":         {nil, "no instances"},
		"no interval":  {[]Instance{noInterval}, "interval must be positive"},
		"no retriever": {[]Instance{noRetriever}, "must have one or two retrievers"},
		"no providers": {[]Instance{noProviders}, "no providers"},
		"nil provider": {[]Instance{nilProvider}, "provider #1 is nil"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := Run(context.Background(), tt.instances, Options{Logger: discardLogger(), Clock: newFakeClock()})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Run error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestSignalContextCancelsOnParent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, stop := SignalContext(parent)
	defer stop()

	cancel()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context was not cancelled")
	}
}
