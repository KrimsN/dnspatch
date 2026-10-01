// Package app is the entry point for building a dnspatch binary of your own,
// for example one that carries a plugin from another module.
//
// A custom main package blank-imports the plugins it wants and calls Main:
//
//	package main
//
//	import (
//		"github.com/dnspatch/dnspatch/app"
//		_ "github.com/dnspatch/dnspatch/plugins/all"
//		_ "example.com/myplugin"
//	)
//
//	func main() { app.Main() }
//
// Which monitoring hooks and notifiers the binary supports is decided by the
// same build tags as for the dnspatch command (ping, notify_all, redis, ...).
// The package exposes only Main and its options; the daemon itself stays
// internal.
package app

import (
	"context"
	"os"

	iapp "github.com/dnspatch/dnspatch/internal/app"
	"github.com/dnspatch/dnspatch/internal/hooks/notify"
	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
)

// hooks builds the per-instance monitoring hooks. It stays nil unless a build
// tag file sets it, which is what makes a build reject ping_url.
var hooks iapp.HookBuilder

type settings struct {
	version string
}

// Option customises Main.
type Option func(*settings)

// WithVersion sets the version that --version prints. Without it the version
// is read from the module build info.
func WithVersion(v string) Option {
	return func(s *settings) { s.version = v }
}

func options(s settings) iapp.Options {
	return iapp.Options{
		Registry: plugin.Default,
		Version:  s.version,
		Hooks:    hooks,
		// Always set: with no backend compiled in, the registry has nothing to
		// build and says so, naming the tag that brings the backend asked for.
		Notify: notify.BuildHook,
	}
}

// Main runs the daemon with the plugins registered in plugin.Default and the
// process arguments, then exits the process with the daemon's exit code. It
// stops the daemon on SIGINT and SIGTERM.
func Main(opts ...Option) {
	var s settings
	for _, o := range opts {
		o(&s)
	}

	ctx, stop := runner.SignalContext(context.Background())
	defer stop()

	code := iapp.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, options(s))
	stop()
	os.Exit(code)
}
