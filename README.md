# dnspatch

A dynamic DNS daemon in Go: it watches your public IP address and patches your DNS records when it changes.

> **Status: early development.** There is no release yet and the public API is not stable. Nothing here is ready to run.

## Design

dnspatch is built around three concepts:

- **Retriever** — reports your current public IP address
- **Provider** — writes that address to a DNS record
- **Instance** — ties one retriever to one or more providers and polls on its own interval

Instances run independently, so several sites or networks can be tracked at once.

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
