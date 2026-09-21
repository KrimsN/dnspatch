# dnspatch

A dynamic DNS daemon in Go: it watches your public IP address and patches your DNS records when it changes.

- One static binary or a container image of a few megabytes, no runtime dependencies.
- Several independent instances in one process: track more than one site, update more than one provider.
- Retrievers and providers are plugins. The plugin contract is a public Go package, so you can add your own without forking.

> **Versioning.** dnspatch follows [semantic versioning](https://semver.org), and it is in the `v0.x` series on purpose: the plugin contract has not been proven by many plugins yet. Until `v1.0.0`, a minor release (`v0.1` to `v0.2`) may change the public API of the `plugin` package and the configuration format; patch releases will not. Breaking changes are called out in the release notes. Pin the version you tested.

## Install

### Binary

Download the archive for your platform from the [releases page](https://github.com/KrimsN/dnspatch/releases): Linux (amd64, arm64, armv7), macOS and Windows (amd64, arm64). Each release carries a `checksums.txt`.

### Docker

The image is built for `linux/amd64`, `linux/arm64` and `linux/arm/v7`. It runs as an unprivileged user (uid 65532) and looks for its configuration at `/etc/dnspatch/config.toml`. The configuration is not baked into the image: mount it, and changing a setting means editing the file and restarting the container, never rebuilding.

With Docker Compose ([compose.yml](compose.yml)):

```sh
cp config.toml.example dnspatch.toml   # edit it
cp .env.example .env                   # put the secrets in it
chmod 644 dnspatch.toml                # the container user must be able to read it
docker compose up -d
```

Every variable in `.env` reaches the container, and `dnspatch.toml` refers to it as `${NAME}`, so secrets stay out of the configuration file. After editing `dnspatch.toml`, run `docker compose restart`. After editing `.env`, run `docker compose up -d`, which recreates the container with the new environment; `restart` does not re-read it.

Without Compose:

```sh
docker run -d --name dnspatch --restart unless-stopped \
  -v "$PWD/dnspatch.toml:/etc/dnspatch/config.toml:ro" \
  --env-file .env \
  ghcr.io/krimsn/dnspatch:latest
```

### From source

```sh
go install github.com/KrimsN/dnspatch/cmd/dnspatch@latest
```

Requires Go 1.24 or newer.

## Quick start

Copy [config.toml.example](config.toml.example) to `dnspatch.toml`, fill it in and start the daemon:

```sh
dnspatch --config dnspatch.toml
```

Without `--config` the daemon uses `$DNSPATCH_CONFIG`, then `./dnspatch.toml`, then `/etc/dnspatch/config.toml`. `dnspatch --version` prints the version.

The daemon exits with code 2 when the configuration is invalid (the problems are listed together, each naming its instance) and with code 1 on a runtime failure. `SIGINT` and `SIGTERM` stop it gracefully.

## Configuration

TOML. DNS record names routinely contain `@` and `*`, which YAML reserves, and plugin parameters are loosely typed — TOML avoids both hazards.

A configuration has three parts: definitions of retrievers, definitions of providers, and instances that combine them.

```toml
interval = "5m"                    # default for every instance, at least 1s

[retriever.ifconfigco]
type = "ifconfigco"

[provider.regru]
type     = "regru"
username = "my-login"
password = "${REGRU_PASSWORD}"     # read from the environment
zone     = "example.com"
rr_name  = "home"

[[instance]]
name = "home"

[instance.retriever]
ref = "ifconfigco"

[[instance.provider]]
ref = "regru"

[[instance.provider]]
ref     = "regru"
rr_name = "*.home"                 # override a parameter of the definition
```

- An instance points at definitions with `ref`. Parameters written next to a `ref` override the definition, except `type`. This is how one provider account serves several records.
- `${NAME}` inside a string is replaced with the environment variable; a variable that is not set is an error, not an empty string. Write `$${` for a literal `${`.
- Unknown parameters are rejected with a hint at the closest known name, so a typo does not go unnoticed.
- Every parameter of every plugin is described in [docs/PARAMETERS.md](docs/PARAMETERS.md).

### Built-in plugins

| Kind | Type | What it does |
|------|------|--------------|
| retriever | `ifconfigco` | asks [ifconfig.co](https://ifconfig.co) for the public address, over IPv4 or IPv6 |
| provider | `regru` | sets the `A` or `AAAA` record of a zone hosted at [REG.RU](https://www.reg.ru), through REG.API 2 |

### Proxies

Some DNS APIs only accept requests from a fixed address, which does not fit a daemon on a dynamic one. Run a proxy on a small host with a static address, allow that address in the provider's API settings, and give the provider a `proxy` parameter:

```toml
[provider.regru]
type  = "regru"
proxy = "${PROXY_URL}"   # for example socks5://user:pass@203.0.113.5:1080
# ...
```

- Supported schemes are `socks5`, `socks5h`, `http` and `https`, with an optional `user:pass@`; percent-encode special characters in them. With `socks5` and `socks5h` the proxy resolves the API host name.
- Every provider and every reference to it can set its own `proxy`, so different zones can leave through different hosts. `proxy = "direct"` means no proxy at all, whatever `HTTP_PROXY` and `HTTPS_PROXY` say.
- Without `proxy`, a provider connects the way Go does by default, so `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` from the environment apply. With a URL or `direct`, the environment is ignored.
- Retrievers take the same parameter, but it defaults to `direct` and they never follow the environment. Behind a proxy the address service reports the address the proxy connects from, not the address of this host, so give a retriever a proxy only when that is the address you want. With a proxy the retriever's `family` no longer pins the connection, it only checks the reply.
- A malformed URL stops the daemon at startup. The URL is never printed in logs or errors, since it may hold a password.

## Behaviour

dnspatch is built around three concepts:

- **Retriever** — reports your current public IP address
- **Provider** — writes that address to a DNS record
- **Instance** — ties one retriever to one or more providers and polls on its own interval

Instances run independently, so several sites or networks can be tracked at once.

### Failure handling

- Providers of an instance are updated independently: one failing provider never stops the others (a stuck one delays the rest of the tick by at most its 30-second deadline).
- A provider is written only when the address differs from the last one it accepted; a failed provider is retried on later ticks, the others are left alone.
- A failing provider is retried with exponential backoff and jitter: the delay is at most the polling interval after the first failure (at least half of it) and its ceiling doubles with every further failure, up to 30 minutes (or the interval, if that is longer).
- Every retrieval and every write has a 30-second deadline.

### State is not persisted

The last written address is kept in memory only. **After a restart the daemon does not know what it wrote before**, so the first tick writes the current address to every provider, even if the record already holds it. A provider that was backing off is tried again immediately. This costs one API call per provider per restart and is intended: a state file would be lost on every restart of a container without a volume, which is exactly where it would be needed, and would bring a path, permissions and a way to be stale of its own. Providers make the write idempotent, so nothing changes when the record is already correct.

## Writing your own plugin

The `plugin` package is public on purpose. Writing a retriever or a provider means implementing a two-method interface and registering it — either upstream in this repository, or in your own module with your own `main`:

```go
type Retriever interface {
	GetIPAddress(ctx context.Context) (netip.Addr, error)
}

type Provider interface {
	SetIPAddress(ctx context.Context, addr netip.Addr) error
}
```

Addresses are passed as `netip.Addr`, so providers pick the record type themselves: `A` for IPv4, `AAAA` for IPv6.

A plugin is a configuration struct plus a constructor; struct tags declare the parameters and feed the generated reference. The step-by-step guide is in [CONTRIBUTING.md](CONTRIBUTING.md#writing-a-plugin).

## License

MIT — see [LICENSE](LICENSE).
