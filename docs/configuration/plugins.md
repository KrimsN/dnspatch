# Built-in plugins

Every parameter of every plugin is described in the [Parameter reference](../PARAMETERS.md), which is generated from the code. This page is the overview.

| Kind | Type | What it does |
|------|------|--------------|
| retriever | `2ip` | asks [2ip.io](https://2ip.io) for the public address (IPv4 only) |
| retriever | `icanhazip` | asks [icanhazip.com](https://icanhazip.com) for the public address, over IPv4, IPv6, or dual |
| retriever | `identme` | asks [ident.me](https://ident.me) for the public address, over IPv4, IPv6, or dual |
| retriever | `ifconfigco` | asks [ifconfig.co](https://ifconfig.co) for the public address, over IPv4, IPv6, or dual |
| retriever | `ipify` | asks [ipify.org](https://www.ipify.org) for the public address, over IPv4, IPv6, or dual |
| retriever | `interface` | reads the address from a local network interface, with no external service; public addresses only, the lowest one if several, narrowed by `network` (see [interface-ipv6.toml](https://github.com/dnspatch/dnspatch/blob/main/examples/interface-ipv6.toml)) |
| provider | `beget` | sets the `A` or `AAAA` record of a zone hosted at [Beget](https://beget.com), through its DNS administration API |
| provider | `cloudflare` | sets the `A` or `AAAA` record of a zone on [Cloudflare](https://cloudflare.com), through its REST API, authenticating with an API token scoped to one zone |
| provider | `duckdns` | sets the `A` or `AAAA` record of a subdomain at [DuckDNS](https://www.duckdns.org), through its own update API; not dyndns2, the token travels in the query string |
| provider | `dyndns2` | updates a record through the dyndns2 protocol of any service that speaks it, for a service that has no plugin of its own; the update URL is a parameter and both addresses go in one request |
| provider | `dyn` | updates the `A` and `AAAA` record of a host at [Dyn](https://dyn.com) (the former DynDNS) through its Dynamic DNS service, both addresses in one request; a `dyndns2` with the URL filled in |
| provider | `dynu` | updates the `A` and `AAAA` record of a host at [Dynu](https://www.dynu.com) through its Dynamic DNS service, in one request; a `dyndns2` with the URL filled in |
| provider | `namecheap` | sets the `A` record of a host at [Namecheap](https://www.namecheap.com) through its Dynamic DNS feature; not dyndns2, IPv4 only, the password travels in the query string |
| provider | `nicru` | updates the `A` and `AAAA` record of a domain at [NIC.RU](https://www.nic.ru) through its Dynamic DNS service, in one request; a `dyndns2` with the URL filled in (see [nicru.toml](https://github.com/dnspatch/dnspatch/blob/main/examples/nicru.toml)) |
| provider | `noip` | updates the `A` and `AAAA` record of a host at [No-IP](https://www.noip.com) through its Dynamic DNS service, in one request; a `dyndns2` with the URL filled in |
| provider | `regru` | sets the `A` or `AAAA` record of a zone hosted at [REG.RU](https://www.reg.ru), through REG.API 2 |
| provider | `rfc2136` | sets the `A` or `AAAA` record on your own name server (BIND, Knot DNS, PowerDNS, Technitium, ...) with RFC 2136 dynamic updates, signed with TSIG |
| provider | `selectel` | sets the `A` or `AAAA` record of a zone hosted at [Selectel](https://selectel.ru) DNS Hosting, through Cloud DNS API v2 |
| provider | `timeweb` | sets the `A` or `AAAA` record of a zone hosted at [Timeweb Cloud](https://timeweb.cloud), through its DNS API, authenticating with a static API token |
| provider | `yandexcloud` | sets the `A` or `AAAA` record of a zone hosted at [Yandex Cloud DNS](https://yandex.cloud/en/services/dns), authenticating as a service account with an authorized key |
| notifier | `redis` | publishes status changes to a Redis Pub/Sub channel (full build) |
| notifier | `rabbitmq` | publishes status changes to a RabbitMQ topic exchange (full build) |
| notifier | `mqtt` | publishes status changes to an MQTT topic (full build) |

Notifiers are described in [Monitoring](../operations/monitoring.md#notifications). Which plugins a build contains, and how to choose them, is covered in [Building from source](../deployment/building.md).
