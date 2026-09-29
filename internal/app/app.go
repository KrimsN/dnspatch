// Package app is the daemon's command-line entry point, shared by every
// dnspatch build. A build's own main package only supplies Options: which
// plugins are registered and, for a build that supports monitoring hooks,
// how to build them from an instance's configuration.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/health"
	"github.com/KrimsN/dnspatch/internal/runner"
	"github.com/KrimsN/dnspatch/plugin"
)

// Exit codes. A configuration problem is told apart from a runtime failure
// so that service managers and scripts can react to them differently.
const (
	ExitOK int = iota
	ExitFailure
	ExitConfig
)

// EnvLogLevel names the environment variable that holds the log level; the
// --log-level flag takes precedence over it.
const EnvLogLevel = "DNSPATCH_LOG_LEVEL"

// HookBuilder builds the runner hooks for one instance from its parsed
// configuration, for example a ping hook when PingURL is set. A build with no
// monitoring hooks passes a nil HookBuilder: Run then rejects a config that
// sets a hook-only field such as PingURL, instead of silently ignoring it.
// log is the daemon's own logger, already tagged with nothing instance-
// specific; a hook that wants to identify itself in the log adds its own
// attributes.
type HookBuilder func(inst config.Instance, log *slog.Logger) ([]runner.Hook, error)

// NotifyBuilder builds the runner.Hook that publishes instances'
// success/failure transitions to an external broker, from one [notify.<name>]
// definition (cfg.Ref is its name, cfg.Type its "type", cfg.Params its other
// parameters). A build with no such backend passes a nil NotifyBuilder: Run
// then rejects a config whose instances use a notifier instead of silently
// ignoring it, the same way an unsupported PingURL is rejected. Unlike Hooks,
// this runs once per definition, not once per instance: each definition is one
// broker connection, built once and attached to every instance that lists it.
type NotifyBuilder func(cfg config.Plugin, log *slog.Logger) (runner.Hook, error)

// Options configures one build of the daemon.
type Options struct {
	// Registry supplies the plugin types this build knows about.
	Registry *plugin.Registry
	// Version is the build's own version, set at build time with -ldflags
	// "-X main.version=...", and passed through as read.
	Version string
	// Hooks builds monitoring hooks per instance. Nil means this build
	// supports none.
	Hooks HookBuilder
	// Notify builds the hook behind each [notify.<name>] definition an
	// instance uses. Nil means this build supports no notify backend.
	Notify NotifyBuilder
}

// healthCheckCommand is the subcommand name a container's HEALTHCHECK runs
// (as "dnspatch healthcheck", the exec form Docker needs in a distroless
// image with no shell): "healthcheck" as the very first argument, ahead of
// the daemon's own flags, exactly as Run's own flag set would otherwise
// reject it as an unrecognized positional argument.
const healthCheckCommand = "healthcheck"

// Run is the daemon's command line: parses flags, loads the config, and
// either starts the daemon or, for --check-config, --version and the
// "healthcheck" subcommand, prints a result and exits without starting it.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, opts Options) int {
	if len(args) > 0 && args[0] == healthCheckCommand {
		return runHealthCheck(args[1:], stdout, stderr)
	}

	flags := flag.NewFlagSet("dnspatch", flag.ContinueOnError)
	flags.SetOutput(stderr)

	configPath := flags.String("config", "", "path to the config file (default: $"+config.EnvPath+", ./dnspatch.toml, /etc/dnspatch/config.toml)")
	logLevel := flags.String("log-level", "", "log level: debug, info, warn or error (default: $"+EnvLogLevel+", then info)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	checkConfig := flags.Bool("check-config", false, "validate the config and exit without starting the daemon")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitConfig
	}

	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "dnspatch", buildVersion(opts.Version))
		return ExitOK
	}

	level, err := parseLogLevel(*logLevel, os.Getenv(EnvLogLevel))
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	path, err := config.ResolvePath(*configPath)
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}

	cfg, err := config.Load(path)
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}

	instances, err := buildInstances(cfg, opts.Registry, opts.Hooks, logger)
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}

	if err := runner.Validate(instances); err != nil {
		return fail(stderr, err, ExitConfig)
	}

	closeNotify, err := attachNotify(instances, cfg, opts.Notify, logger)
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}
	defer closeNotify()

	if *checkConfig {
		printConfigSummary(stdout, path, cfg, instances, opts.Registry)
		return ExitOK
	}

	// Every build gets the health-status hook, unconditionally: unlike
	// PingURL, this is not something the config opts into, and it adds no
	// dependency worth keeping out of the lightweight build. A directory it
	// cannot write to (for example a read-only container filesystem with no
	// writable mount for it) only disables it; the DNS updates themselves
	// must never depend on it.
	healthDir := health.ResolveDir()
	if recorder, err := health.NewRecorder(healthDir, logger); err != nil {
		logger.Warn("healthcheck disabled: could not prepare the health directory", "dir", healthDir, "err", err)
	} else {
		for i := range instances {
			instances[i].Hooks = append(instances[i].Hooks, recorder)
		}
	}

	logger.Info("starting", "version", buildVersion(opts.Version), "config", path, "instances", len(instances))

	if err := runner.Run(ctx, instances, runner.Options{Logger: logger}); err != nil {
		return fail(stderr, err, ExitFailure)
	}

	logger.Info("stopped")

	return ExitOK
}

// parseLogLevel picks the log level: the flag value if set, otherwise the
// environment value, otherwise info.
func parseLogLevel(flagValue, envValue string) (slog.Level, error) {
	name, source := flagValue, "--log-level"
	if name == "" {
		name, source = envValue, EnvLogLevel
	}
	if name == "" {
		return slog.LevelInfo, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("%s: %q is not a log level, use debug, info, warn or error", source, name)
	}

	return level, nil
}

// attachNotify builds one hook per notifier definition that an instance uses
// and appends to each instance the hooks of the notifiers it lists, so its
// status changes reach those brokers and no others. A definition shared by
// several instances is built once. Problems in all the definitions are reported
// together. The returned function closes the connections the hooks hold; it is
// safe to call even when err is set, and does nothing when no instance uses a
// notifier. instances is in the order of cfg.Instances.
func attachNotify(instances []runner.Instance, cfg config.Config, build NotifyBuilder, log *slog.Logger) (closeAll func(), err error) {
	closeAll = func() {}

	if len(cfg.Notify) == 0 {
		return closeAll, nil
	}

	names := slices.Sorted(maps.Keys(cfg.Notify))

	if build == nil {
		first := cfg.Notify[names[0]]

		return closeAll, fmt.Errorf("notify: notifier %q (type %q) is used, but this build does not support a notify backend; use a build with the notify_all tag (the -full image or binary)", names[0], first.Type)
	}

	var errs []error

	hooks := make(map[string]runner.Hook, len(names))

	for _, name := range names {
		table := cfg.Notify[name]

		hook, err := build(table, log)
		if err != nil {
			errs = append(errs, fmt.Errorf("notify %q (%s): %w", name, table.Type, err))
			continue
		}

		hooks[name] = hook
	}

	closeAll = func() {
		for _, hook := range hooks {
			if closer, ok := hook.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return closeAll, err
	}

	for i, in := range cfg.Instances {
		for _, name := range in.Notify {
			instances[i].Hooks = append(instances[i].Hooks, hooks[name])
		}
	}

	return closeAll, nil
}

// buildInstances turns the parsed configuration into runnable instances by
// building every plugin through the registry. All problems are reported
// together, each naming the instance it belongs to. When buildHooks is nil,
// an instance that sets PingURL is a config error: the field means nothing
// in a build with no hooks, and ignoring it silently would leave monitoring
// quietly disabled.
func buildInstances(cfg config.Config, registry *plugin.Registry, buildHooks HookBuilder, log *slog.Logger) ([]runner.Instance, error) {
	var errs []error

	instances := make([]runner.Instance, 0, len(cfg.Instances))

	for _, in := range cfg.Instances {
		built := runner.Instance{Name: in.Name, Interval: in.Interval}

		for _, r := range in.Retrievers {
			retriever, err := registry.BuildRetriever(r.Type, r.Params)
			if err != nil {
				errs = append(errs, fmt.Errorf("instance %q: %w", in.Name, err))
				continue
			}

			family, _ := r.Params["family"].(string)
			built.Retrievers = append(built.Retrievers, runner.NamedRetriever{
				Name:      r.Name(),
				Retriever: retriever,
				Family:    strings.ToLower(family),
			})
		}

		for _, p := range in.Providers {
			provider, err := registry.BuildProvider(p.Type, p.Params)
			if err != nil {
				errs = append(errs, fmt.Errorf("instance %q: %w", in.Name, err))
				continue
			}

			built.Providers = append(built.Providers, runner.NamedProvider{Name: p.Name(), Provider: provider})
		}

		if in.PingURL == "" {
			// Nothing to wire, in any build.
		} else if buildHooks == nil {
			errs = append(errs, fmt.Errorf("instance %q: ping_url is set, but this build does not support monitoring hooks; use a build with the ping tag (the -full image or binary)", in.Name))
		} else if hooks, err := buildHooks(in, log); err != nil {
			errs = append(errs, fmt.Errorf("instance %q: %w", in.Name, err))
		} else {
			built.Hooks = hooks
		}

		instances = append(instances, built)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return instances, nil
}

// fail reports a problem unambiguously as an error, prefixed apart from
// dnspatch's other stderr output (a plain "dnspatch: <message>" that a first-
// time user has no successful run to compare against), and returns code.
func fail(stderr io.Writer, err error, code int) int {
	_, _ = fmt.Fprintln(stderr, "dnspatch: error:", err)
	return code
}

// runHealthCheck implements the "healthcheck" subcommand: it re-reads the
// same config a running daemon uses, so it knows every instance's own
// interval, and reports whether each one's status file (written by
// health.Recorder) is fresh enough. It never starts the daemon or the
// instances themselves.
func runHealthCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dnspatch healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)

	configPath := flags.String("config", "", "path to the config file (default: $"+config.EnvPath+", ./dnspatch.toml, /etc/dnspatch/config.toml)")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitConfig
	}

	path, err := config.ResolvePath(*configPath)
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}

	cfg, err := config.Load(path)
	if err != nil {
		return fail(stderr, err, ExitConfig)
	}

	if err := health.CheckAll(health.ResolveDir(), cfg.Instances, time.Now()); err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch: unhealthy:", err)
		return ExitFailure
	}

	_, _ = fmt.Fprintln(stdout, "dnspatch: healthy")
	return ExitOK
}

// buildVersion reports the version set at build time, falling back to the
// module version recorded by `go install`.
func buildVersion(version string) string {
	if version == "" {
		version = "dev"
	}
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}
