package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

// errAttemptTimeout is the cancellation cause of an attempt that ran out of
// time, as opposed to one interrupted by shutdown.
var errAttemptTimeout = errors.New("attempt timed out")

// providerState is what an instance remembers about one provider. It lives in
// memory only.
type providerState struct {
	name     string
	provider plugin.Provider

	// last is the address most recently written successfully. It is the zero
	// value after a failed write, when the record's content is unknown.
	last netip.Addr
	// failures counts consecutive failed writes.
	failures int
	// next is the earliest moment the provider may be tried again.
	next time.Time
}

// instance polls one retriever and keeps its providers up to date.
type instance struct {
	name      string
	interval  time.Duration
	timeout   time.Duration
	retriever plugin.Retriever
	providers []*providerState

	clock   Clock
	log     *slog.Logger
	backoff backoff
	jitter  func(n int64) int64
}

func newInstance(cfg Instance, opts Options) *instance {
	log := opts.Logger.With("instance", cfg.Name)
	providers := make([]*providerState, len(cfg.Providers))
	for i, p := range cfg.Providers {
		providers[i] = &providerState{name: p.Name, provider: p.Provider}
	}
	return &instance{
		name:      cfg.Name,
		interval:  cfg.Interval,
		timeout:   opts.AttemptTimeout,
		retriever: cfg.Retriever,
		providers: providers,
		clock:     opts.Clock,
		log:       log,
		backoff:   newBackoff(cfg.Interval),
		jitter:    rand.Int64N,
	}
}

// run ticks immediately and then once per interval until ctx is cancelled.
func (in *instance) run(ctx context.Context) {
	ticker := in.clock.NewTicker(in.interval)
	defer ticker.Stop()

	in.log.Info("instance started", "interval", in.interval)
	// Every failure is logged per provider inside tick, so the returned error
	// is not reported again.
	for ctx.Err() == nil {
		_ = in.tick(ctx)
		select {
		case <-ctx.Done():
		case <-ticker.C():
		}
	}
	in.log.Info("instance stopped")
}

// tick fetches the current address and writes it to every provider that
// needs it. Providers are visited independently: a failure of one does not
// stop the others, and all failures are returned joined.
func (in *instance) tick(ctx context.Context) error {
	start := in.clock.Now()

	var addr netip.Addr
	err := in.attempt(ctx, func(ctx context.Context) (err error) {
		addr, err = in.retriever.GetIPAddress(ctx)
		return err
	})
	if err == nil && !addr.IsValid() {
		err = errors.New("retriever returned an invalid address")
	}
	if err != nil {
		if ctx.Err() != nil {
			in.log.Info("retrieving address interrupted")
			return nil
		}
		in.log.Warn("retrieving address failed", "err", err)
		return fmt.Errorf("retriever: %w", err)
	}

	var errs []error
	for _, p := range in.providers {
		if ctx.Err() != nil {
			break
		}
		if err := in.update(ctx, p, addr, start); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// update writes addr to one provider unless it already holds it or is still
// backing off. The tick start, not the end of the attempt, anchors the next
// allowed attempt so a slow attempt does not eat into the retry delay.
func (in *instance) update(ctx context.Context, p *providerState, addr netip.Addr, start time.Time) error {
	log := in.log.With("provider", p.name)
	if p.last == addr {
		log.Debug("address unchanged, skipping", "addr", addr)
		return nil
	}
	if start.Before(p.next) {
		log.Debug("backing off, skipping", "addr", addr, "retry_at", p.next)
		return nil
	}

	err := in.attempt(ctx, func(ctx context.Context) error {
		return p.provider.SetIPAddress(ctx, addr)
	})
	if err == nil {
		p.last = addr
		p.failures = 0
		p.next = time.Time{}
		log.Info("address updated", "addr", addr)
		return nil
	}
	if ctx.Err() != nil {
		log.Info("update interrupted", "addr", addr)
		return nil
	}

	p.last = netip.Addr{}
	p.failures++
	delay := in.backoff.delay(p.failures, in.jitter)
	p.next = start.Add(delay)
	log.Warn("update failed", "addr", addr, "err", err, "failures", p.failures, "retry_in", delay)
	return fmt.Errorf("provider %q: %w", p.name, err)
}

// attempt runs fn under the per-attempt deadline. A deadline overrun is
// reported as errAttemptTimeout wrapping whatever fn returned.
func (in *instance) attempt(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := in.clock.AfterFunc(in.timeout, func() { cancel(errAttemptTimeout) })
	defer timer.Stop()

	err := fn(ctx)
	if err != nil && errors.Is(context.Cause(ctx), errAttemptTimeout) {
		return fmt.Errorf("%w after %s: %w", errAttemptTimeout, in.timeout, err)
	}
	return err
}
