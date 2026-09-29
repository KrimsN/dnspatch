// Package health implements the file-marker liveness check the daemon and
// the "healthcheck" subcommand share: a status file per instance, touched on
// every completed cycle (successful or not), used by a container's
// HEALTHCHECK to tell a hung or crashed daemon apart from one busy backing
// off a failing provider, without shell or curl in a distroless image.
package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/runner"
)

// EnvDir names the environment variable that overrides the status directory.
const EnvDir = "DNSPATCH_HEALTH_DIR"

// StalenessFactor multiplies an instance's interval to get how old its status
// file may be before it is considered stuck. A cycle completes, successfully
// or not, once per interval regardless of how a provider is behaving, so a
// file older than a small multiple of the interval means the tick loop
// itself stopped, not that a provider is merely failing.
const StalenessFactor = 2

// MinStaleness floors the staleness threshold, so a very short interval still
// gives a slow attempt (up to runner.DefaultAttemptTimeout per retriever or
// provider) room to finish before it is flagged.
const MinStaleness = 30 * time.Second

// DefaultDir is the status directory used when EnvDir is not set.
func DefaultDir() string {
	return filepath.Join(os.TempDir(), "dnspatch-health")
}

// ResolveDir picks the status directory: EnvDir if set, otherwise DefaultDir.
func ResolveDir() string {
	if dir := os.Getenv(EnvDir); dir != "" {
		return dir
	}
	return DefaultDir()
}

// Recorder is a runner.Hook that touches one status file per instance after
// every completed cycle. A failure to write it (for example a read-only
// filesystem with no writable mount for dir) is logged and otherwise
// ignored: it must never affect the DNS update loop itself.
type Recorder struct {
	dir string
	log *slog.Logger
}

// NewRecorder prepares dir to receive status files. dir is created if it
// does not exist yet.
func NewRecorder(dir string, log *slog.Logger) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("health directory %q: %w", dir, err)
	}

	return &Recorder{dir: dir, log: log}, nil
}

// AfterCycle implements runner.Hook.
func (r *Recorder) AfterCycle(_ context.Context, ev runner.CycleEvent) {
	path := filepath.Join(r.dir, fileName(ev.Instance))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		r.log.Warn("could not record health status", "instance", ev.Instance, "err", err)
	}
}

// CheckAll reports, for every instance, whether its status file exists and
// is fresh enough, as of now. Problems are joined together, one per
// instance, so healthcheck can report every stuck instance at once.
func CheckAll(dir string, instances []config.Instance, now time.Time) error {
	var errs []error

	for _, in := range instances {
		if err := check(dir, in, now); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func check(dir string, in config.Instance, now time.Time) error {
	path := filepath.Join(dir, fileName(in.Name))

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("instance %q: no completed cycle recorded yet: %w", in.Name, err)
	}

	threshold := in.Interval * StalenessFactor
	if threshold < MinStaleness {
		threshold = MinStaleness
	}

	if age := now.Sub(info.ModTime()); age > threshold {
		return fmt.Errorf("instance %q: last completed cycle was %s ago, want at most %s", in.Name, age.Round(time.Second), threshold)
	}

	return nil
}

// fileName turns an instance name into a safe file name: instance names come
// from the operator's own config, but are not otherwise restricted, and must
// not be read as a path (for example a name containing "/", or exactly "."
// or ".."). PathEscape takes care of "/" and other reserved characters, but
// leaves "." and ".." untouched since both are valid path segments on their
// own; a leading "_" rules those out without needing them escaped.
func fileName(instance string) string {
	escaped := url.PathEscape(instance)
	if escaped == "" || escaped == "." || escaped == ".." {
		escaped = "_" + escaped
	}

	return escaped
}
