// Package runner orchestrates instances: each one runs in its own goroutine
// on its own interval and stops when the context is cancelled.
//
// Providers of an instance are visited independently and their errors are
// collected with errors.Join. The last address written is tracked per
// provider, and a failing provider is retried with exponential backoff and
// jitter. State is kept in memory only and does not survive a restart, so the
// first tick after start writes to every provider.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/KrimsN/dnspatch/plugin"
)

// DefaultAttemptTimeout bounds a single retrieval or a single provider write.
const DefaultAttemptTimeout = 30 * time.Second

// NamedProvider is a provider of an instance; the name identifies it in logs.
type NamedProvider struct {
	Name     string
	Provider plugin.Provider
}

// NamedRetriever is a retriever of an instance; the name identifies it in logs.
type NamedRetriever struct {
	Name      string
	Retriever plugin.Retriever
}

// Instance ties one or more retrievers to the providers they feed and the
// interval on which it is polled. There is at most one retriever per address
// family: two retrievers means one for IPv4 and one for IPv6, distinguished
// at run time by the address each returns.
type Instance struct {
	Name       string
	Interval   time.Duration
	Retrievers []NamedRetriever
	Providers  []NamedProvider
}

// Options tune the runner; the zero value is ready to use.
type Options struct {
	// Logger receives all runner output. Defaults to slog.Default().
	Logger *slog.Logger
	// Clock is the source of time. Defaults to the system clock.
	Clock Clock
	// AttemptTimeout bounds every retrieval and every provider write: when it
	// expires the attempt's context is cancelled and the call is expected to
	// return promptly, as the plugin contract requires. The runner waits for
	// the call and does not abandon it, so a plugin that ignores its context
	// blocks its instance. Defaults to DefaultAttemptTimeout.
	AttemptTimeout time.Duration
}

// Run starts all instances and blocks until ctx is cancelled and every
// instance has stopped. It returns an error only if the instances are
// invalid; cancelling ctx is a normal way to stop and returns nil.
//
// A panic in a plugin is not recovered: it terminates the whole process, as a
// panic in any goroutine does. A daemon that keeps running with a plugin in an
// unknown state would be worse than one restarted by its service manager, for
// example by Docker's restart policy.
func Run(ctx context.Context, instances []Instance, opts Options) error {
	if err := Validate(instances); err != nil {
		return err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = DefaultAttemptTimeout
	}

	var wg sync.WaitGroup
	for _, cfg := range instances {
		in := newInstance(nameRetrievers(nameProviders(cfg)), opts)
		wg.Add(1)
		go func() {
			defer wg.Done()
			in.run(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// SignalContext returns a context cancelled by SIGINT or SIGTERM. The returned
// function releases the signal handler.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// Validate checks that instances can be run: there is at least one, and each has
// a positive interval, one or two retrievers and providers. All problems are
// reported together. Run performs the same check itself; calling Validate
// first lets a caller tell an invalid setup apart from a failure while
// running.
func Validate(instances []Instance) error {
	if len(instances) == 0 {
		return errors.New("no instances to run")
	}
	var errs []error
	for _, in := range instances {
		if in.Interval <= 0 {
			errs = append(errs, fmt.Errorf("instance %q: interval must be positive", in.Name))
		}
		if len(in.Retrievers) == 0 || len(in.Retrievers) > 2 {
			errs = append(errs, fmt.Errorf("instance %q: must have one or two retrievers, has %d", in.Name, len(in.Retrievers)))
		}
		for i, r := range in.Retrievers {
			if r.Retriever == nil {
				errs = append(errs, fmt.Errorf("instance %q: retriever #%d is nil", in.Name, i+1))
			}
		}
		if len(in.Providers) == 0 {
			errs = append(errs, fmt.Errorf("instance %q: no providers", in.Name))
		}
		for i, p := range in.Providers {
			if p.Provider == nil {
				errs = append(errs, fmt.Errorf("instance %q: provider #%d is nil", in.Name, i+1))
			}
		}
	}
	return errors.Join(errs...)
}

// nameProviders gives unnamed providers a positional name for the logs.
func nameProviders(cfg Instance) Instance {
	providers := make([]NamedProvider, len(cfg.Providers))
	for i, p := range cfg.Providers {
		if p.Name == "" {
			p.Name = fmt.Sprintf("#%d", i+1)
		}
		providers[i] = p
	}
	cfg.Providers = providers
	return cfg
}

// nameRetrievers gives unnamed retrievers a positional name for the logs.
func nameRetrievers(cfg Instance) Instance {
	retrievers := make([]NamedRetriever, len(cfg.Retrievers))
	for i, r := range cfg.Retrievers {
		if r.Name == "" {
			r.Name = fmt.Sprintf("#%d", i+1)
		}
		retrievers[i] = r
	}
	cfg.Retrievers = retrievers
	return cfg
}
