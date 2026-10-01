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
// and docs/deployment/building.md has the table. The release binaries and images come in two
// flavours: this lightweight one and the -full one, built with -tags
// "ping,notify_all".
package main

import (
	"github.com/dnspatch/dnspatch/app"
	_ "github.com/dnspatch/dnspatch/plugins/all"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(app.WithVersion(version))
}
