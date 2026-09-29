// Command dnspatch is a dynamic DNS daemon: it watches the public IP address
// and patches DNS records when it changes.
//
// A plain "go build" gives the lightweight build: no monitoring hooks and no
// notify backends, so a config that sets ping_url or [[notify]] is rejected.
// Optional features are compiled in with build tags, each in its own file so
// that its dependencies stay out of a build that does not ask for them:
//
//	ping        the ping_url hook (Healthchecks.io, Uptime Kuma push)
//	redis       the [[notify]] backend publishing to Redis Pub/Sub
//	notify_all  every notify backend
//
// The release binaries and images come in two flavours: this lightweight one
// and the -full one, built with -tags "ping,notify_all".
package main

import (
	"context"
	"os"

	"github.com/KrimsN/dnspatch/internal/app"
	"github.com/KrimsN/dnspatch/internal/hooks/notify"
	"github.com/KrimsN/dnspatch/internal/runner"
	"github.com/KrimsN/dnspatch/plugin"
	_ "github.com/KrimsN/dnspatch/plugins/all"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// hooks builds the per-instance monitoring hooks. It stays nil unless a build
// tag file sets it, which is what makes the build reject ping_url.
var hooks app.HookBuilder

func options() app.Options {
	return app.Options{
		Registry: plugin.Default,
		Version:  version,
		Hooks:    hooks,
		// Always set: with no backend compiled in, notify.Default has nothing
		// to build and says so, naming the backends this build does have.
		Notify: notify.BuildHook,
	}
}

func main() {
	ctx, stop := runner.SignalContext(context.Background())
	defer stop()

	os.Exit(app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, options()))
}
