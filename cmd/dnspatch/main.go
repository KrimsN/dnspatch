// Command dnspatch is a dynamic DNS daemon: it watches the public IP address
// and patches DNS records when it changes.
//
// A plain "go build" gives the lightweight build: every retriever and provider,
// but no monitoring hooks and no notifiers, so a config that sets ping_url or
// publishes to a [notify.<name>] notifier is rejected. What goes into a build
// is chosen with build tags:
//
//	ping           the ping_url hook (Healthchecks.io, Uptime Kuma push)
//	notify_all     every notifier backend
//	dnspatch_none  no retriever and no provider, except the ones named below
//	providers_all  every provider, with dnspatch_none
//	retrievers_all every retriever, with dnspatch_none
//	<plugin>       one plugin, by its type name: redis, cloudflare, ipify, ...
//
// The tag of a plugin comes from plugins/all, which cmd/genplugins generates,
// and README.md has the table. The release binaries and images come in two
// flavours: this lightweight one and the -full one, built with -tags
// "ping,notify_all".
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
		// Always set: with no backend compiled in, the registry has nothing to
		// build and says so, naming the tag that brings the backend asked for.
		Notify: notify.BuildHook,
	}
}

func main() {
	ctx, stop := runner.SignalContext(context.Background())
	defer stop()

	os.Exit(app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, options()))
}
