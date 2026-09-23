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

	// lastV4 and lastV6 are the addresses most recently written successfully,
	// per family. Each is the zero value after a failed write or before a
	// family has ever been sent, when that part of the record is unknown.
	lastV4, lastV6 netip.Addr
	// failures counts consecutive failed writes.
	failures int
	// next is the earliest moment the provider may be tried again.
	next time.Time
}

// instance polls one or more retrievers, in order, until every address
// family is filled, and keeps its providers up to date.
type instance struct {
	name       string
	interval   time.Duration
	timeout    time.Duration
	retrievers []NamedRetriever
	providers  []*providerState

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
		name:       cfg.Name,
		interval:   cfg.Interval,
		timeout:    opts.AttemptTimeout,
		retrievers: cfg.Retrievers,
		providers:  providers,
		clock:      opts.Clock,
		log:        log,
		backoff:    newBackoff(cfg.Interval),
		jitter:     rand.Int64N,
	}
}

// intervalAdvisor is implemented by retrievers whose service asks not to be
// polled more often than once per the returned interval. It is optional and
// matched structurally, so a plugin does not import this package to provide it.
// The runner only warns about a shorter interval and still honours it.
type intervalAdvisor interface {
	RecommendedInterval() time.Duration
}

// warnShortInterval logs a warning when the polling interval is shorter than
// a retriever's service asks for.
func (in *instance) warnShortInterval() {
	for _, r := range in.retrievers {
		advisor, ok := r.Retriever.(intervalAdvisor)
		if !ok {
			continue
		}
		if advised := advisor.RecommendedInterval(); in.interval < advised {
			in.log.Warn("interval is shorter than the retriever's service allows, requests may be rejected or the address blocked",
				"retriever", r.Name, "interval", in.interval, "recommended", advised)
		}
	}
}

// run ticks immediately and then once per interval until ctx is cancelled.
func (in *instance) run(ctx context.Context) {
	ticker := in.clock.NewTicker(in.interval)
	defer ticker.Stop()

	in.log.Info("instance started", "interval", in.interval)
	in.warnShortInterval()
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

// tick fetches the current address of every retriever and writes the result
// to every provider that needs it. Retrievers and providers are each visited
// independently: a failure of one does not stop the others, and all failures
// are returned joined.
func (in *instance) tick(ctx context.Context) error {
	start := in.clock.Now()

	addrs, errs := in.retrieve(ctx)
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.Join(errs...)
	}

	for _, p := range in.providers {
		if ctx.Err() != nil {
			break
		}
		if err := in.update(ctx, p, addrs, start); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// retrieve fetches addresses from the retrievers in order, one after the
// other, until every family is filled or the list is exhausted: a retriever
// is not even called once both families already have an address, so N
// retrievers take up to N times the per-attempt timeout only in the worst
// case where every family stays unfilled until the last one. A failing
// retriever is logged and skipped; the others are still tried. A retriever
// reporting a family that an earlier one already filled is a redundant
// fallback source, not an error: its value for that family is ignored.
func (in *instance) retrieve(ctx context.Context) (plugin.Addresses, []error) {
	var addrs plugin.Addresses

	var errs []error
	for _, r := range in.retrievers {
		if ctx.Err() != nil {
			break
		}
		if addrs.V4.IsValid() && addrs.V6.IsValid() {
			break
		}

		var got plugin.Addresses
		err := in.attempt(ctx, func(ctx context.Context) (err error) {
			got, err = r.Retriever.GetAddresses(ctx)
			return err
		})
		if err == nil && !got.V4.IsValid() && !got.V6.IsValid() {
			err = errors.New("retriever returned no valid address")
		}
		if err != nil {
			if ctx.Err() != nil {
				in.log.Info("retrieving address interrupted", "retriever", r.Name)
				continue
			}
			in.log.Warn("retrieving address failed", "retriever", r.Name, "err", err)
			errs = append(errs, fmt.Errorf("retriever %q: %w", r.Name, err))
			continue
		}

		if got.V4.IsValid() {
			if addrs.V4.IsValid() {
				in.log.Debug("ipv4 address already filled by an earlier retriever, ignoring", "retriever", r.Name)
			} else {
				addrs.V4 = got.V4
			}
		}
		if got.V6.IsValid() {
			if addrs.V6.IsValid() {
				in.log.Debug("ipv6 address already filled by an earlier retriever, ignoring", "retriever", r.Name)
			} else {
				addrs.V6 = got.V6
			}
		}
	}

	return addrs, errs
}

// update writes the families of addrs that changed to one provider, unless
// none changed or the provider is still backing off. The tick start, not the
// end of the attempt, anchors the next allowed attempt so a slow attempt does
// not eat into the retry delay.
func (in *instance) update(ctx context.Context, p *providerState, addrs plugin.Addresses, start time.Time) error {
	log := in.log.With("provider", p.name)

	toSend := plugin.Addresses{}
	if addrs.V4.IsValid() && addrs.V4 != p.lastV4 {
		toSend.V4 = addrs.V4
	}
	if addrs.V6.IsValid() && addrs.V6 != p.lastV6 {
		toSend.V6 = addrs.V6
	}
	if !toSend.V4.IsValid() && !toSend.V6.IsValid() {
		log.Debug("address unchanged, skipping", "addrs", addrs)
		return nil
	}
	if start.Before(p.next) {
		log.Debug("backing off, skipping", "addrs", toSend, "retry_at", p.next)
		return nil
	}

	err := in.attempt(ctx, func(ctx context.Context) error {
		return p.provider.Update(ctx, toSend, plugin.RecordOptions{})
	})
	if err == nil {
		if toSend.V4.IsValid() {
			p.lastV4 = toSend.V4
		}
		if toSend.V6.IsValid() {
			p.lastV6 = toSend.V6
		}
		p.failures = 0
		p.next = time.Time{}
		log.Info("address updated", "addrs", toSend)
		return nil
	}
	if ctx.Err() != nil {
		log.Info("update interrupted", "addrs", toSend)
		return nil
	}

	p.lastV4 = netip.Addr{}
	p.lastV6 = netip.Addr{}
	p.failures++
	delay := in.backoff.delay(p.failures, in.jitter)
	p.next = start.Add(delay)
	log.Warn("update failed", "addrs", toSend, "err", err, "failures", p.failures, "retry_in", delay)
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
