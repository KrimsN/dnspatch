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

// Instance ties one retriever to the providers it feeds and the interval on
// which it is polled.
type Instance struct {
	Name      string
	Interval  time.Duration
	Retriever plugin.Retriever
	Providers []NamedProvider
}

// Options tune the runner; the zero value is ready to use.
type Options struct {
	// Logger receives all runner output. Defaults to slog.Default().
	Logger *slog.Logger
	// Clock is the source of time. Defaults to the system clock.
	Clock Clock
	// AttemptTimeout bounds every retrieval and every provider write, so a
	// plugin that ignores its own timeouts cannot block an instance forever.
	// Defaults to DefaultAttemptTimeout.
	AttemptTimeout time.Duration
}

// Run starts all instances and blocks until ctx is cancelled and every
// instance has stopped. It returns an error only if the instances are
// invalid; cancelling ctx is a normal way to stop and returns nil.
func Run(ctx context.Context, instances []Instance, opts Options) error {
	if err := validate(instances); err != nil {
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
		in := newInstance(nameProviders(cfg), opts)
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

func validate(instances []Instance) error {
	if len(instances) == 0 {
		return errors.New("no instances to run")
	}
	var errs []error
	for _, in := range instances {
		if in.Interval <= 0 {
			errs = append(errs, fmt.Errorf("instance %q: interval must be positive", in.Name))
		}
		if in.Retriever == nil {
			errs = append(errs, fmt.Errorf("instance %q: no retriever", in.Name))
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
