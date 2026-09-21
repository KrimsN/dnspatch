# dnspatch

A dynamic DNS daemon in Go: it watches your public IP address and patches your DNS records when it changes.

> **Status: early development.** There is no release yet and the public API is not stable. Nothing here is ready to run.

## Design

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

The last written address is kept in memory only. After a restart the first tick writes the current address to every provider, and a provider that was backing off is tried again immediately. This costs one API call per provider per restart and is intended.

## Extensibility

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

## Configuration

TOML. DNS record names routinely contain `@` and `*`, which YAML reserves, and plugin parameters are loosely typed — TOML avoids both hazards.

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

## License

MIT — see [LICENSE](LICENSE).
