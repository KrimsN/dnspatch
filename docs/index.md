# ![dnspatch](assets/logo-wordmark.svg#only-light){ width="320" }![dnspatch](assets/logo-wordmark-dark.svg#only-dark){ width="320" }

A dynamic DNS daemon in Go: it watches your public IP address and patches your DNS records when it changes.

- One static binary or a container image of a few megabytes, no runtime dependencies.
- Several independent instances in one process: track more than one site, update more than one provider.
- Retrievers and providers are plugins. The plugin contract is a public Go package, so you can add your own without forking.

## How it works

dnspatch is built around three concepts:

- **Retriever**: reports your current public IP address.
- **Provider**: writes that address to a DNS record.
- **Instance**: ties one or more retrievers to one or more providers and polls on its own interval.

Instances run independently, so several sites or networks can be tracked at once. See [Behaviour](operations/behaviour.md) for what happens when something fails.

## Where to go next

| I want to... | Read |
|--------------|------|
| install the daemon | [Installation](installation.md) |
| get a first working config | [Quick start](quick-start.md) |
| understand the configuration file | [Configuration overview](configuration/index.md) |
| see what a provider or retriever accepts | [Parameter reference](PARAMETERS.md) |
| run it in a container | [Docker](deployment/docker.md) |
| build a small binary for a router | [Building from source](deployment/building.md) |
| get alerted when updates fail | [Monitoring](operations/monitoring.md) |
| add support for another DNS host | [Writing a plugin](development/writing-a-plugin.md) |
| upgrade to a new release | [Migrations](migrations/0.2-to-0.3.md) |

!!! note "Versioning"
    dnspatch follows [semantic versioning](https://semver.org), and it is in the `v0.x` series on purpose: the plugin contract has not been proven by many plugins yet. Until `v1.0.0`, a minor release (`v0.1` to `v0.2`) may change the public API of the `plugin` package and the configuration format; patch releases will not. Breaking changes are called out in the release notes and described in the [migration guides](migrations/0.2-to-0.3.md). Pin the version you tested.

## Links

[Releases](https://github.com/dnspatch/dnspatch/releases) ·
[Docker Hub](https://hub.docker.com/r/krimsn/dnspatch) ·
[GitHub Container Registry](https://github.com/dnspatch/dnspatch/pkgs/container/dnspatch) ·
[Go API reference](https://pkg.go.dev/github.com/dnspatch/dnspatch) ·
[Issues](https://github.com/dnspatch/dnspatch/issues)
