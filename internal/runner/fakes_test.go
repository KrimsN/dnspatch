package runner

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeClock is a Clock whose time only moves when a test calls Advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock  *fakeClock
	when   time.Time
	period time.Duration // zero for one-shot timers
	ch     chan time.Time
	fn     func()
	active bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: epoch} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(d time.Duration) Ticker {
	return fakeTicker{c.add(&fakeTimer{period: d, when: c.Now().Add(d), ch: make(chan time.Time, 1)})}
}

// fakeTicker adapts fakeTimer to Ticker, whose Stop has no result.
type fakeTicker struct{ *fakeTimer }

func (t fakeTicker) Stop() { t.fakeTimer.Stop() }

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	return c.add(&fakeTimer{when: c.Now().Add(d), fn: f})
}

func (c *fakeClock) add(t *fakeTimer) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t.clock, t.active = c, true
	c.timers = append(c.timers, t)
	return t
}

// Advance moves time forward, firing every timer that falls due on the way in
// chronological order.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.now.Add(d)
	for {
		var next *fakeTimer
		for _, t := range c.timers {
			if t.active && !t.when.After(target) && (next == nil || t.when.Before(next.when)) {
				next = t
			}
		}
		if next == nil {
			break
		}
		c.now = next.when
		if next.period > 0 {
			select {
			case next.ch <- c.now:
			default:
			}
			next.when = next.when.Add(next.period)
			continue
		}
		next.active = false
		c.mu.Unlock()
		next.fn()
		c.mu.Lock()
	}
	c.now = target
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := t.active
	t.active = false
	return was
}

// fakeRetriever returns a configurable Addresses or error, and counts how
// many times it was called so a test can check the fallback early exit.
type fakeRetriever struct {
	mu    sync.Mutex
	addrs plugin.Addresses
	err   error
	calls int
}

// singleFamily builds the Addresses a real single-family retriever would
// return for addr: V4 or V6, whichever addr belongs to.
func singleFamily(addr string) plugin.Addresses {
	a := netip.MustParseAddr(addr)
	if a.Is4() {
		return plugin.Addresses{V4: a}
	}
	return plugin.Addresses{V6: a}
}

func newFakeRetriever(addr string) *fakeRetriever {
	return &fakeRetriever{addrs: singleFamily(addr)}
}

// set reconfigures the retriever to report a single address, as if it only
// ever handled one family.
func (r *fakeRetriever) set(addr string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addrs, r.err = singleFamily(addr), err
}

// setBoth reconfigures the retriever to report both families in one call, as
// a family="both" plugin does.
func (r *fakeRetriever) setBoth(v4, v6 string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addrs = plugin.Addresses{V4: netip.MustParseAddr(v4), V6: netip.MustParseAddr(v6)}
	r.err = nil
}

func (r *fakeRetriever) GetAddresses(context.Context) (plugin.Addresses, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.addrs, r.err
}

// callCount reports how many times GetAddresses was called, to check that
// the fallback loop stops calling retrievers once every family is filled.
func (r *fakeRetriever) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// fakeProvider records every write and fails or blocks on demand.
type fakeProvider struct {
	mu     sync.Mutex
	writes []netip.Addr
	// updates records every call's full Addresses, for dual-stack tests.
	updates []plugin.Addresses
	// fail is consulted with the 1-based call number; a non-nil result is
	// returned to the runner.
	fail func(call int) error
	// block makes the provider wait for its context instead of returning.
	block  bool
	called chan netip.Addr
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{called: make(chan netip.Addr, 1000)}
}

func (p *fakeProvider) Update(ctx context.Context, addrs plugin.Addresses, _ plugin.RecordOptions) error {
	addr := addrs.V4
	if !addr.IsValid() {
		addr = addrs.V6
	}

	p.mu.Lock()
	p.writes = append(p.writes, addr)
	p.updates = append(p.updates, addrs)
	call := len(p.writes)
	fail, block := p.fail, p.block
	p.mu.Unlock()
	p.called <- addr

	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if fail != nil {
		return fail(call)
	}
	return nil
}

func (p *fakeProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.writes)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestInstance builds a single-retriever instance on a fake clock with
// silent logging and no random jitter.
func newTestInstance(clock Clock, interval time.Duration, r *fakeRetriever, providers ...*fakeProvider) *instance {
	return newTestInstanceMulti(clock, interval, []*fakeRetriever{r}, providers...)
}

// newTestInstanceMulti builds an instance with any number of retrievers, in
// the given order, on a fake clock with silent logging and no random jitter.
func newTestInstanceMulti(clock Clock, interval time.Duration, retrievers []*fakeRetriever, providers ...*fakeProvider) *instance {
	cfg := Instance{Name: "test", Interval: interval}
	for i, r := range retrievers {
		cfg.Retrievers = append(cfg.Retrievers, NamedRetriever{Name: string(rune('r' + i)), Retriever: r})
	}
	for i, p := range providers {
		cfg.Providers = append(cfg.Providers, NamedProvider{Name: string(rune('a' + i)), Provider: p})
	}
	in := newInstance(cfg, Options{Logger: discardLogger(), Clock: clock, AttemptTimeout: DefaultAttemptTimeout})
	in.jitter = func(int64) int64 { return 0 }
	return in
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the event")
		panic("unreachable")
	}
}

// settle waits until no attempt deadline is pending, that is until every
// in-flight retrieval or write has finished. Tests call it before Advance so
// that the advance cannot fire a deadline of a call still on its way out.
func (c *fakeClock) settle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.pendingDeadlines() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("attempts did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func (c *fakeClock) pendingDeadlines() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if t.active && t.period == 0 {
			n++
		}
	}
	return n
}
