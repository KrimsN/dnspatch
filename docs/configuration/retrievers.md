# Retrievers and address families

An instance accepts one or more `[[instance.retriever]]` tables, polled in order until every address family is filled. Which family a retriever reports is decided by the address it actually returns, not by configuration.

## Dual-stack with two retrievers

Dual-stack (one A and one AAAA record from the same instance) needs two retrievers, one per family, for example two `ifconfigco` retrievers with `family = "ipv4"` and `family = "ipv6"`:

```toml
[retriever.v4]
type   = "ifconfigco"
family = "ipv4"

[retriever.v6]
type   = "ifconfigco"
family = "ipv6"

[[instance]]
name = "home"

[[instance.retriever]]
ref = "v4"

[[instance.retriever]]
ref = "v6"

[[instance.provider]]
ref = "regru"
```

## One retriever for both families

A retriever whose service is itself dual-stack can report both families in one call with `family = "dual"`, supported by `icanhazip`, `identme`, `ifconfigco`, `ipify` and `interface`:

```toml
[retriever.home]
type   = "ipify"
family = "dual"
```

## Fallback

A third or later retriever is a fallback source, tried only for the families the earlier ones did not fill; it is never called once every family already has an address. Two retrievers reporting the same family (for example, two independent sources both configured with `family = "ipv4"`) is a valid fallback chain, not a misconfiguration: the first one to succeed wins, and the others are skipped for that family.

The retriever's own `family` parameter also tells the instance which families to even look for: an instance whose retrievers are all `family = "ipv4"` never tries to retrieve an IPv6 address, and a retriever pinned to a family that is already filled (by an earlier one, `dual` or otherwise) is skipped without being called. This also builds a fallback chain per family out of retrievers with different roles, for example:

```toml
[retriever.icanhazip]
type   = "icanhazip"
family = "dual"

[retriever.ipify]
type   = "ipify"
family = "ipv6"

[retriever.ifconfigco]
type   = "ifconfigco"
family = "ipv4"

[[instance]]
name = "home"

[[instance.retriever]]
ref = "icanhazip"

[[instance.retriever]]
ref = "ipify"

[[instance.retriever]]
ref = "ifconfigco"

[[instance.provider]]
ref = "regru"
```

Here `icanhazip` is tried first for both families; if it succeeds, `ipify` and `ifconfigco` are never called. If it fails, `ipify` is tried for IPv6 and `ifconfigco` for IPv4.

Only a retriever type that has its own `family` parameter (`icanhazip`, `identme`, `ifconfigco`, `ipify`, `interface`) can be pinned this way; one that does not, such as `2ip`, is always treated like `dual`: a candidate for whichever family is still missing, decided by the address it actually returns.

## Reading the address from an interface

The `interface` retriever reads the address from a local network interface, with no external service. It reports public addresses only, the lowest one if several, narrowed by `network`. See [interface-ipv6.toml](https://github.com/dnspatch/dnspatch/blob/main/examples/interface-ipv6.toml) for a delegated IPv6 prefix.
