// Command dnspatch is a dynamic DNS daemon: it watches the public IP address
// and patches DNS records when it changes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/runner"
	"github.com/KrimsN/dnspatch/plugin"
	_ "github.com/KrimsN/dnspatch/plugins/all"
)

// Exit codes. A configuration problem is told apart from a runtime failure
// so that service managers and scripts can react to them differently.
const (
	exitOK int = iota
	exitFailure
	exitConfig
)

// envLogLevel names the environment variable that holds the log level; the
// --log-level flag takes precedence over it.
const envLogLevel = "DNSPATCH_LOG_LEVEL"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := runner.SignalContext(context.Background())
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, plugin.Default))
}

// run is main without the process-wide parts, so that tests can drive it.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, registry *plugin.Registry) int {
	flags := flag.NewFlagSet("dnspatch", flag.ContinueOnError)
	flags.SetOutput(stderr)

	configPath := flags.String("config", "", "path to the config file (default: $"+config.EnvPath+", ./dnspatch.toml, /etc/dnspatch/config.toml)")
	logLevel := flags.String("log-level", "", "log level: debug, info, warn or error (default: $"+envLogLevel+", then info)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	checkConfig := flags.Bool("check-config", false, "validate the config and exit without starting the daemon")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}

	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "dnspatch", buildVersion())
		return exitOK
	}

	level, err := parseLogLevel(*logLevel, os.Getenv(envLogLevel))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitConfig
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	path, err := config.ResolvePath(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitConfig
	}

	cfg, err := config.Load(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitConfig
	}

	instances, err := buildInstances(cfg, registry)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitConfig
	}

	if err := runner.Validate(instances); err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitConfig
	}

	if *checkConfig {
		printConfigSummary(stdout, path, instances)
		return exitOK
	}

	logger.Info("starting", "version", buildVersion(), "config", path, "instances", len(instances))

	if err := runner.Run(ctx, instances, runner.Options{Logger: logger}); err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitFailure
	}

	logger.Info("stopped")

	return exitOK
}

// parseLogLevel picks the log level: the flag value if set, otherwise the
// environment value, otherwise info.
func parseLogLevel(flagValue, envValue string) (slog.Level, error) {
	name, source := flagValue, "--log-level"
	if name == "" {
		name, source = envValue, envLogLevel
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

// buildInstances turns the parsed configuration into runnable instances by
// building every plugin through the registry. All problems are reported
// together, each naming the instance it belongs to.
func buildInstances(cfg config.Config, registry *plugin.Registry) ([]runner.Instance, error) {
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

			name := r.Ref
			if name == "" {
				name = r.Type
			}
			family, _ := r.Params["family"].(string)
			built.Retrievers = append(built.Retrievers, runner.NamedRetriever{
				Name:      name,
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

			name := p.Ref
			if name == "" {
				name = p.Type
			}
			built.Providers = append(built.Providers, runner.NamedProvider{Name: name, Provider: provider})
		}

		instances = append(instances, built)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return instances, nil
}

// printConfigSummary confirms that path was read and parsed as intended: one
// line per instance naming its interval and the retrievers and providers it
// resolved to, so the user can tell the parsed config apart from a typo that
// silently fell back to a default (an empty ref, a misspelled family, ...).
func printConfigSummary(stdout io.Writer, path string, instances []runner.Instance) {
	_, _ = fmt.Fprintf(stdout, "dnspatch: config OK: %s (%d instance(s))\n", path, len(instances))

	for _, in := range instances {
		_, _ = fmt.Fprintf(stdout, "  %s: interval=%s retrievers=%s providers=%s\n",
			in.Name, in.Interval, describeRetrievers(in.Retrievers), describeProviders(in.Providers))
	}
}

// describeRetrievers renders an instance's retrievers as "name(family)",
// omitting the family when it was not set.
func describeRetrievers(retrievers []runner.NamedRetriever) string {
	names := make([]string, len(retrievers))
	for i, r := range retrievers {
		if r.Family == "" {
			names[i] = r.Name
		} else {
			names[i] = fmt.Sprintf("%s(%s)", r.Name, r.Family)
		}
	}

	return "[" + strings.Join(names, ", ") + "]"
}

// describeProviders renders an instance's providers by name.
func describeProviders(providers []runner.NamedProvider) string {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Name
	}

	return "[" + strings.Join(names, ", ") + "]"
}

// buildVersion reports the version set at build time, falling back to the
// module version recorded by `go install`.
func buildVersion() string {
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}
