# dnspatch

[![CI](https://img.shields.io/github/actions/workflow/status/dnspatch/dnspatch/ci.yml?branch=main&label=CI)](https://github.com/dnspatch/dnspatch/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dnspatch/dnspatch)](https://github.com/dnspatch/dnspatch/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/dnspatch/dnspatch.svg)](https://pkg.go.dev/github.com/dnspatch/dnspatch)
[![Docker pulls](https://img.shields.io/docker/pulls/krimsn/dnspatch)](https://hub.docker.com/r/krimsn/dnspatch)
[![License](https://img.shields.io/github/license/dnspatch/dnspatch)](LICENSE)
[![Hits](https://hits.sh/github.com/dnspatch/dnspatch.svg)](https://hits.sh/github.com/dnspatch/dnspatch/)

A dynamic DNS daemon in Go: it watches your public IP address and patches your DNS records when it changes.

[Documentation](https://dnspatch.github.io/dnspatch/) · [Releases](https://github.com/dnspatch/dnspatch/releases) · [Docker Hub](https://hub.docker.com/r/krimsn/dnspatch) · [GitHub Container Registry](https://github.com/dnspatch/dnspatch/pkgs/container/dnspatch) · [API reference](https://pkg.go.dev/github.com/dnspatch/dnspatch) · [Issues](https://github.com/dnspatch/dnspatch/issues)

- One static binary or a container image of a few megabytes, no runtime dependencies.
- Several independent instances in one process: track more than one site, update more than one provider.
- Retrievers and providers are plugins. The plugin contract is a public Go package, so you can add your own without forking.

> **Versioning.** dnspatch follows [semantic versioning](https://semver.org), and it is in the `v0.x` series on purpose: the plugin contract has not been proven by many plugins yet. Until `v1.0.0`, a minor release (`v0.1` to `v0.2`) may change the public API of the `plugin` package and the configuration format; patch releases will not. Breaking changes are called out in the release notes and described in the [migration guides](https://dnspatch.github.io/dnspatch/migrations/0.2-to-0.3/). Pin the version you tested.

## Install

Download a binary from the [releases page](https://github.com/dnspatch/dnspatch/releases) (Linux, macOS and Windows), or run the container image:

```sh
docker run -d --name dnspatch --restart unless-stopped   -v "$PWD/dnspatch.toml:/etc/dnspatch/config.toml:ro"   --env-file .env   krimsn/dnspatch:latest
```

The image is published to Docker Hub (`krimsn/dnspatch`) and the GitHub Container Registry (`ghcr.io/dnspatch/dnspatch`). Or build from source with Go 1.25 or newer:

```sh
go install github.com/dnspatch/dnspatch/cmd/dnspatch@latest
```

Docker Compose, the `-full` build with monitoring, build tags for a smaller binary and cross-compiling are in the [documentation](https://dnspatch.github.io/dnspatch/installation/).

## Quick start

A configuration defines providers and instances; an instance ties retrievers to providers:

```toml
[provider.regru]
type     = "regru"
zone     = "example.com"
rr_name  = "home"
username = "my-login"
password = "${PASSWORD}"     # read from the environment

[[instance]]
name = "home"

[[instance.retriever]]
type = "ifconfigco"

[[instance.provider]]
ref = "regru"
```

```sh
dnspatch --config dnspatch.toml
```

`dnspatch --check-config` validates the file without starting the daemon.

## Documentation

The full documentation is at **[dnspatch.github.io/dnspatch](https://dnspatch.github.io/dnspatch/)**:

- [Quick start](https://dnspatch.github.io/dnspatch/quick-start/) and [configuration](https://dnspatch.github.io/dnspatch/configuration/): instances, dual-stack, fallback between retrievers, secrets, proxies
- [Built-in plugins](https://dnspatch.github.io/dnspatch/configuration/plugins/) and the [parameter reference](https://dnspatch.github.io/dnspatch/PARAMETERS/) of every retriever and provider
- [Docker](https://dnspatch.github.io/dnspatch/deployment/docker/) and [building from source](https://dnspatch.github.io/dnspatch/deployment/building/) with build tags
- [Monitoring](https://dnspatch.github.io/dnspatch/operations/monitoring/): health check, pings, notifications
- [Writing a plugin](https://dnspatch.github.io/dnspatch/development/writing-a-plugin/)

Ready-made configs for specific scenarios are in [examples/](examples/).

## Contributing

Bug reports and ideas go to [GitHub Issues](https://github.com/dnspatch/dnspatch/issues). [CONTRIBUTING.md](CONTRIBUTING.md) covers commit messages and pull requests; the documentation sources are in [docs/](docs/).

## License

MIT — see [LICENSE](LICENSE).
