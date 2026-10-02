<h1 align="center"><img src="docs/assets/logo.svg" alt="dnspatch" width="360"></h1>

[![CI](https://img.shields.io/github/actions/workflow/status/dnspatch/dnspatch/ci.yml?branch=main&label=CI)](https://github.com/dnspatch/dnspatch/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dnspatch/dnspatch)](https://github.com/dnspatch/dnspatch/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/dnspatch/dnspatch.svg)](https://pkg.go.dev/github.com/dnspatch/dnspatch)
[![Docker pulls](https://img.shields.io/docker/pulls/krimsn/dnspatch)](https://hub.docker.com/r/krimsn/dnspatch)
[![License](https://img.shields.io/github/license/dnspatch/dnspatch)](LICENSE)

**Keep your DNS records pointed at your home or server IP, even when your ISP changes it.**

dnspatch is a dynamic DNS daemon in Go. It watches your public IPv4 and IPv6 address and updates your DNS records when it changes. It is a single static binary or a container image of a few megabytes, with no runtime dependencies.

[Documentation](https://dnspatch.github.io/dnspatch/) · [Releases](https://github.com/dnspatch/dnspatch/releases) · [Docker Hub](https://hub.docker.com/r/krimsn/dnspatch) · [GitHub Container Registry](https://github.com/dnspatch/dnspatch/pkgs/container/dnspatch) · [API reference](https://pkg.go.dev/github.com/dnspatch/dnspatch) · [Issues](https://github.com/dnspatch/dnspatch/issues)

## Why dnspatch

- **Many providers, one tool.** [Cloudflare, DuckDNS, No-IP, Dynu, Namecheap, your own BIND or PowerDNS over RFC 2136, any dyndns2 service](#supported-providers) and several regional hosts (REG.RU, NIC.RU, Selectel, Timeweb, Yandex Cloud, Beget).
- **No single point of failure for the lookup.** Several retrievers per instance with fallback between them (ipify, icanhazip, ifconfig.co, ident.me, 2ip, or a local network interface), so one flaky lookup service doesn't break updates.
- **IPv4, IPv6 and dual-stack.** `A` and `AAAA` records are handled independently.
- **Several sites in one process.** Independent instances, each with its own interval, retrievers and providers.
- **Knows when something breaks.** A container health check, pings to Healthchecks.io or Uptime Kuma, and events published to Redis, RabbitMQ or MQTT for your own alerts. A failing provider backs off instead of hammering an API.
- **Secrets stay out of the config.** `${ENV}` substitution and `${file:...}` for Docker and Kubernetes secrets.
- **Extensible without forking.** Retrievers, providers and notifiers are plugins, and the plugin contract is a public Go package, so you can add your own in your own module.

> **Versioning.** dnspatch follows [semantic versioning](https://semver.org) and is in the `v0.x` series on purpose: the plugin contract has not been proven by many plugins yet. Until `v1.0.0`, a minor release may change the public API of the `plugin`, `httpx` and `app` packages and the configuration format; patch releases will not. Breaking changes are called out in the release notes and described in the [migration guides](https://dnspatch.github.io/dnspatch/migrations/0.2-to-0.3/). Pin the version you tested.

## Quick start

1. Create `dnspatch.toml`. This keeps `home.example.com` on Cloudflare pointed at your current public IP:

    ```toml
    [[instance]]
    name = "home"

    [[instance.retriever]]
    type = "ipify"

    [[instance.provider]]
    type    = "cloudflare"
    zone_id = "YOUR_ZONE_ID"
    zone    = "example.com"
    rr_name = "home"
    token   = "${CF_TOKEN}"   # read from the environment
    ```

2. Put the secret in `.env`, which stays out of the config file:

    ```sh
    CF_TOKEN=your-api-token-scoped-to-Zone-DNS-Edit
    ```

3. Create `compose.yml` next to them:

    ```yaml
    services:
      dnspatch:
        image: krimsn/dnspatch:latest
        restart: unless-stopped
        env_file: .env
        volumes:
          - ./dnspatch.toml:/etc/dnspatch/config.toml:ro
    ```

4. Run it:

    ```sh
    docker compose up -d
    ```

`dnspatch --check-config` validates the file without starting the daemon. Ready-made configs for dual-stack, fallback between retrievers, several providers, proxies, secrets from files and your own name server are in [examples/](examples/). The [compose.yml](compose.yml) in the repository root is a fuller version with log rotation and a read-only filesystem.

## Install

- **Binary** for Linux, macOS and Windows from the [releases page](https://github.com/dnspatch/dnspatch/releases).
- **Container image** from Docker Hub (`krimsn/dnspatch`) or the GitHub Container Registry (`ghcr.io/dnspatch/dnspatch`). The `latest-full` tag adds monitoring pings and notifiers.
- **From source**, with Go 1.25 or newer:

    ```sh
    go install github.com/dnspatch/dnspatch/cmd/dnspatch@latest
    ```

    This builds the lightweight version: without the `ping_url` hook and without notifiers (Redis, RabbitMQ, MQTT). To get them, add build tags:

    ```sh
    go install -tags "ping,notify_all" github.com/dnspatch/dnspatch/cmd/dnspatch@latest
    ```

Docker Compose, build tags for a smaller binary and cross-compiling are in the [installation guide](https://dnspatch.github.io/dnspatch/installation/).

## Supported providers

| Provider | Records | Notes |
|----------|---------|-------|
| Cloudflare | A, AAAA | API token scoped to one zone, optional proxying |
| DuckDNS | A, AAAA | |
| No-IP, Dyn, Dynu | A, AAAA | dyndns2-based |
| Namecheap | A | IPv4 only |
| `dyndns2` | A, AAAA | any service that speaks the dyndns2 protocol |
| `rfc2136` | A, AAAA | your own BIND, Knot, PowerDNS, Technitium; TSIG-signed |
| REG.RU, NIC.RU, Selectel, Timeweb Cloud, Yandex Cloud DNS, Beget | A, AAAA | |

The full list with every parameter is in the [parameter reference](https://dnspatch.github.io/dnspatch/PARAMETERS/). Your provider isn't there? Use `dyndns2` if it speaks that protocol, or [write a plugin](https://dnspatch.github.io/dnspatch/development/writing-a-plugin/).

## Documentation

The full documentation is at **[dnspatch.github.io/dnspatch](https://dnspatch.github.io/dnspatch/)**:

- [Quick start](https://dnspatch.github.io/dnspatch/quick-start/) and [configuration](https://dnspatch.github.io/dnspatch/configuration/): instances, dual-stack, fallback between retrievers, secrets, proxies
- [Built-in plugins](https://dnspatch.github.io/dnspatch/configuration/plugins/) and the [parameter reference](https://dnspatch.github.io/dnspatch/PARAMETERS/) of every retriever and provider
- [Docker](https://dnspatch.github.io/dnspatch/deployment/docker/) and [building from source](https://dnspatch.github.io/dnspatch/deployment/building/) with build tags
- [Monitoring](https://dnspatch.github.io/dnspatch/operations/monitoring/): health check, pings, notifications
- [Writing a plugin](https://dnspatch.github.io/dnspatch/development/writing-a-plugin/)
- [Plugins outside the repository](https://dnspatch.github.io/dnspatch/development/external-plugins/): your own module and binary

Ready-made configs for specific scenarios are in [examples/](examples/).

## Contributing

Bug reports and ideas go to [GitHub Issues](https://github.com/dnspatch/dnspatch/issues). [CONTRIBUTING.md](CONTRIBUTING.md) covers commit messages and pull requests; the documentation sources are in [docs/](docs/).

## License

MIT — see [LICENSE](LICENSE).
