# Plugins outside the repository

A plugin does not have to live in this repository. You can write it in your own module and build a `dnspatch` binary that carries it, the way `xcaddy` does for Caddy. Three public packages make that possible:

| Package | What it is for |
|---|---|
| `plugin` | the contract: `Retriever`, `Provider`, `Notifier`, and `RegisterRetriever` / `RegisterProvider` / `RegisterNotifier` |
| `httpx` | the same HTTP client and `proxy` parameters the built-in plugins use: `ProxyConfig`, `DirectProxyConfig`, `NewClient`, `NewTransport`, `ParseProxy`, `IsDirect`, `ValidateBaseURL`, `Snippet` |
| `app` | the entry point: `app.Main(opts ...app.Option)` runs the daemon with whatever plugins your binary registered |

The configuration loader, the runner and the hooks stay internal.

!!! warning "Stability in v0.x"
    The three packages above are the public API, and nothing else in the module is. While the version is `v0.x`, a minor release may still change them, and the change is listed in the release notes. Pin the version in your `go.mod`.

## The module

```text
mydnspatch/
├── go.mod
├── main.go
└── myplugin/
    └── myplugin.go
```

The plugin is written as described in [Writing a plugin](writing-a-plugin.md): a `Config` struct with `toml`, `default` and `doc` tags, an implementation of the interface, and a `plugin.Register...` call in `init`. Embed `httpx.DirectProxyConfig` (or `httpx.ProxyConfig`) in the config to get the `proxy` parameter, and build the client with `httpx.NewClient`.

```go
package myplugin

import (
	"context"
	"net/netip"

	"github.com/dnspatch/dnspatch/httpx"
	"github.com/dnspatch/dnspatch/plugin"
)

type Config struct {
	Address string `toml:"address" default:"203.0.113.1" doc:"Address to report"`

	httpx.DirectProxyConfig
}

type retriever struct{ addr netip.Addr }

func (r retriever) GetAddresses(context.Context) (plugin.Addresses, error) {
	return plugin.Addresses{V4: r.addr}, nil
}

func init() {
	plugin.RegisterRetriever("fixed", func(cfg Config) (plugin.Retriever, error) {
		addr, err := netip.ParseAddr(cfg.Address)
		if err != nil {
			return nil, err
		}

		return retriever{addr}, nil
	})
}
```

## Your own `main.go`

The binary blank-imports the plugins it wants and calls `app.Main`:

```go
package main

import (
	"github.com/dnspatch/dnspatch/app"
	_ "github.com/dnspatch/dnspatch/plugins/all"

	_ "example.com/mydnspatch/myplugin"
)

func main() {
	app.Main(app.WithVersion("1.0.0"))
}
```

`plugins/all` brings every built-in plugin and honours the [build tags](../deployment/building.md); leave it out for a binary with your plugin alone, or import single plugin packages instead. `app.WithVersion` sets what `--version` prints; without it the version comes from the module build info.

## Building

```bash
go mod tidy
go build -o mydnspatch .
```

The tags work as for the stock binary: `-tags ping` adds the `ping_url` hook, `-tags notify_all` every notifier, `-tags dnspatch_none` drops the built-in retrievers and providers.

```bash
./mydnspatch --check-config --config dnspatch.toml
```

`--check-config` validates the parameters of your plugin like any other, including its `default` and `doc` tags, and a config that names an unknown type is rejected.

The generators (`go generate`, `cmd/gendoc`, `cmd/genplugins`) work on this repository only, so the parameter reference and the build-tag table do not list external plugins. Document the parameters of your plugin where you publish it.

## Contributing instead

If the plugin is of use to others, a pull request to this repository is the easier route: see [CONTRIBUTING.md](https://github.com/dnspatch/dnspatch/blob/main/CONTRIBUTING.md) for commit and pull request conventions.
