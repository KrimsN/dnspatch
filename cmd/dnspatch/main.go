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
	showVersion := flags.Bool("version", false, "print the version and exit")

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

	logger := slog.New(slog.NewTextHandler(stderr, nil))

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

	logger.Info("starting", "version", buildVersion(), "config", path, "instances", len(instances))

	if err := runner.Run(ctx, instances, runner.Options{Logger: logger}); err != nil {
		_, _ = fmt.Fprintln(stderr, "dnspatch:", err)
		return exitFailure
	}

	logger.Info("stopped")

	return exitOK
}

// buildInstances turns the parsed configuration into runnable instances by
// building every plugin through the registry. All problems are reported
// together, each naming the instance it belongs to.
func buildInstances(cfg config.Config, registry *plugin.Registry) ([]runner.Instance, error) {
	var errs []error

	instances := make([]runner.Instance, 0, len(cfg.Instances))

	for _, in := range cfg.Instances {
		built := runner.Instance{Name: in.Name, Interval: in.Interval}

		retriever, err := registry.BuildRetriever(in.Retriever.Type, in.Retriever.Params)
		if err != nil {
			errs = append(errs, fmt.Errorf("instance %q: %w", in.Name, err))
		}
		built.Retriever = retriever

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
