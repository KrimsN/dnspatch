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

## License

MIT — see [LICENSE](LICENSE).
