# Configuration overview

TOML. DNS record names routinely contain `@` and `*`, which YAML reserves, and plugin parameters are loosely typed: TOML avoids both hazards.

A configuration has three parts: definitions of retrievers, definitions of providers, and instances that combine them. A fourth, optional part, `[notify.<name>]`, describes notifiers (see [Monitoring](../operations/monitoring.md)).

```toml
interval = "5m"                    # default for every instance, at least 1s

[provider.regru]
type     = "regru"
zone     = "example.com"
rr_name  = "home"
username = "my-login"
password = "${PASSWORD}"     # read from the environment

[[instance]]
name = "home"

[[instance.retriever]]
type = "ifconfigco"                # inline: not shared, so no [retriever.<name>] block

[[instance.provider]]
ref = "regru"

[[instance.provider]]
ref     = "regru"
rr_name = "*.home"                 # override a parameter of the definition
```

## `ref` and inline `type`

An instance points at a definition with `ref`, or declares the plugin inline with `type`. `ref` and `type` are mutually exclusive.

- `ref` names a `[retriever.<name>]` or `[provider.<name>]` block; parameters written next to `ref` override the definition, except `type`. This is how one provider account serves several records, or one retriever definition serves several instances.
- `type` builds the plugin from the instance table alone, with no definition to merge in. Use it for a retriever or provider that only one instance needs: most retrievers, and any provider not shared across records.

## Short form

When a definition is used as it is, with nothing overridden, an instance lists its name instead of a table: `"name"` is the same as `{ ref = "name" }`. Names and inline tables can be mixed in one array:

```toml
[[instance]]
name      = "home"
retriever = ["ipify"]
provider  = ["regru", { ref = "regru", rr_name = "*.home" }]
```

The `[[instance.retriever]]` and `[[instance.provider]]` form keeps working. TOML does not allow one key to be written both ways in the same instance, but different keys can: `retriever = ["ipify"]` goes together with `[[instance.provider]]`. The order of the elements is kept; for retrievers it is the polling order. A name cannot carry overrides or `type`: use a table for that.

## Instances

An instance accepts one or more retrievers (`[[instance.retriever]]` tables or names, see above), polled in order until every address family is filled. Which family a retriever reports is decided by the address it actually returns, not by configuration. Dual-stack setups and fallback chains are described on [Retrievers and address families](retrievers.md).

An instance also takes one or more providers (`[[instance.provider]]` tables or names). Providers of an instance are updated independently: see [Behaviour](../operations/behaviour.md).

## Values in strings

- `${NAME}` inside a string is replaced with the environment variable; a variable that is not set is an error, not an empty string. Write `$${` for a literal `${`.
- `${file:/path}` is replaced with the contents of the file, minus one trailing newline; this is how Docker and Kubernetes secrets, mounted as files, reach the config. An unreadable file is an error.

See [Secrets](secrets.md) for how to use them.

## Validation

Unknown parameters are rejected with a hint at the closest known name, so a typo does not go unnoticed. Use [`dnspatch --check-config`](../quick-start.md#check-the-configuration) to validate a file without starting the daemon.

Every parameter of every plugin is described in the [Parameter reference](../PARAMETERS.md).
