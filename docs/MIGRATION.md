# Migration guide

dnspatch is `v0.x`: the configuration format and the plugin API may change
between minor versions. This page lists what breaks and how to adapt.

## 0.2 → 0.3

Two things break: the `[instance.retriever]` table in the configuration and
the `Retriever` and `Provider` interfaces of `plugin`. Everything else in 0.3
is additive.

### Configuration: `[instance.retriever]` is now an array

An instance takes one or more retrievers, so the table became an array of
tables. The old form is rejected with
`"retriever" must be an array of tables: [[instance.retriever]]`.

Before:

```toml
[[instance]]
domain = "home.example.com"

  [instance.retriever]
  ref = "ipify"

  [[instance.provider]]
  ref = "regru"
```

After:

```toml
[[instance]]
domain = "home.example.com"

  [[instance.retriever]]
  ref = "ipify"

  [[instance.provider]]
  ref = "regru"
```

Retrievers are polled in order until every address family is filled, so a
second retriever is either a fallback or the source of the other family
(dual-stack). See [`examples/`](../examples/README.md).

### Configuration: what is new and needs no action

- `type` may be set instead of `ref` in `[[instance.retriever]]` and
  `[[instance.provider]]`, which builds the plugin from that table alone. The
  two are mutually exclusive.
- Any parameter can read a secret from a file: `${file:PATH}`. A literal `${`
  is still written `$${`.
- `family = "dual"` on `ipify`, `icanhazip`, `identme` and `ifconfigco` fetches
  both families with one retriever.
- `dnspatch --check-config` validates a configuration without starting the
  daemon.
- New plugins: retriever `interface`; providers `beget`, `rfc2136`,
  `yandexcloud`.

### Plugin API: `Retriever`

`GetIPAddress` is replaced by `GetAddresses`, which returns both families.

Before:

```go
func (r *Retriever) GetIPAddress(ctx context.Context) (netip.Addr, error)
```

After:

```go
func (r *Retriever) GetAddresses(ctx context.Context) (plugin.Addresses, error)
```

`plugin.Addresses` is `struct{ V4, V6 netip.Addr }`. Fill the family you found
and leave the other at its zero value; at least one field must be valid.
A retriever that only ever knows one family does:

```go
return plugin.Addresses{V4: addr}, nil // or V6: addr
```

### Plugin API: `Provider`

`SetIPAddress` is replaced by `Update`, which receives both families and the
record options.

Before:

```go
func (p *Provider) SetIPAddress(ctx context.Context, addr netip.Addr) error
```

After:

```go
func (p *Provider) Update(ctx context.Context, addrs plugin.Addresses, opts plugin.RecordOptions) error
```

- An invalid (zero) `V4` or `V6` means that family is left untouched: do not
  delete or change the existing record of that type. The runner sends only the
  families that changed since the last successful write.
- Both fields can be valid in one call: write the `A` and the `AAAA` record.
  If the service cannot do both in one request, do them one after another and
  return an error if either fails.
- `opts.TTL`, when positive, overrides the provider's configured TTL. A
  provider whose service has no TTL ignores it.

The simplest port of a single-family provider keeps the old body and calls it
per family:

```go
func (p *Provider) Update(ctx context.Context, addrs plugin.Addresses, opts plugin.RecordOptions) error {
	for _, addr := range []netip.Addr{addrs.V4, addrs.V6} {
		if !addr.IsValid() {
			continue
		}
		if err := p.set(ctx, addr); err != nil {
			return err
		}
	}
	return nil
}
```

Tests that called `SetIPAddress` or `GetIPAddress` must be updated the same
way. Run `go generate ./...` afterwards if the plugin configuration changed.
