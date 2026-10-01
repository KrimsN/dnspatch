# Examples

The [examples/](https://github.com/dnspatch/dnspatch/tree/main/examples) directory has self-contained configs for specific scenarios. Each file is a complete `dnspatch.toml`: copy it, replace the `CHANGE_ME` placeholders and the `${...}` references, and start the daemon with `--config`. CI checks every file with `--check-config`, so they stay valid. The files below are included from the repository, so what you see is what CI checks.

| File | Shows |
|------|-------|
| [`minimal.toml`](#minimaltoml) | The smallest working config: one retriever, one provider, one instance. Start here. |
| [`dual-stack.toml`](#dual-stacktoml) | One instance writing both an A and an AAAA record, using two retrievers pinned to `ipv4` and `ipv6`. |
| [`family-dual-with-fallback.toml`](#family-dual-with-fallbacktoml) | A retriever whose service is itself dual-stack (`family = "dual"`), plus a fallback chain across further retrievers for the families it does not fill. |
| [`interface-ipv6.toml`](#interface-ipv6toml) | Reading the IPv6 address from a network interface instead of an external service, with a delegated prefix picked by `network`. |
| [`nicru.toml`](#nicrutoml) | Updating a record at NIC.RU with one request for both addresses; notes the NIC.RU quirk of changing same-name records in every zone of the contract. Other dyndns2 services use `type = "dyndns2"` with a `base_url`. |
| [`proxy.toml`](#proxytoml) | Reaching a provider's API through a proxy, for a DNS host that only accepts requests from a fixed address. |
| [`rfc2136.toml`](#rfc2136toml) | Updating a record on your own name server (BIND, Knot, PowerDNS, ...) with RFC 2136 dynamic updates signed with a TSIG key. |
| [`secrets-from-files.toml`](#secrets-from-filestoml) | Reading a secret from a file (`${file:/path}`) instead of an environment variable, the way Docker/Kubernetes secrets are mounted. |
| [`multi-provider.toml`](#multi-providertoml) | Several instances and providers in one process: two sites, two DNS hosts, independent polling intervals. |
| [`full-build.toml`](#full-buildtoml) | The features that only exist in the full build: `ping_url` (a ping on every cycle) and `[notify.<name>]` (events published to a broker: status changes by default, more with `events`, per instance). |

For the parameters of every plugin see the [Parameter reference](../PARAMETERS.md); the general syntax is in the [Configuration overview](index.md).

## The files

### minimal.toml

```toml
--8<-- "examples/minimal.toml"
```

### dual-stack.toml

```toml
--8<-- "examples/dual-stack.toml"
```

### family-dual-with-fallback.toml

```toml
--8<-- "examples/family-dual-with-fallback.toml"
```

### interface-ipv6.toml

```toml
--8<-- "examples/interface-ipv6.toml"
```

### nicru.toml

```toml
--8<-- "examples/nicru.toml"
```

### proxy.toml

```toml
--8<-- "examples/proxy.toml"
```

### rfc2136.toml

```toml
--8<-- "examples/rfc2136.toml"
```

### secrets-from-files.toml

```toml
--8<-- "examples/secrets-from-files.toml"
```

### multi-provider.toml

```toml
--8<-- "examples/multi-provider.toml"
```

### full-build.toml

```toml
--8<-- "examples/full-build.toml"
```

