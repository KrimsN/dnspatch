# Configuration overview

TOML. DNS record names routinely contain `@` and `*`, which YAML reserves, and plugin parameters are loosely typed: TOML avoids both hazards.

A configuration has three parts: definitions of retrievers, definitions of providers, and instances that combine them. A fourth, optional part, `[notify.<name>]`, describes notifiers and the events they publish (see [Monitoring](../operations/monitoring.md#notifications)).

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

## Declaring retrievers and providers

An instance gets its retrievers and providers in one of three ways. All of them produce the same result; they differ in how much you write and whether the object can be reused.

### Inline declaration

The plugin is described in the instance itself, with `type`. Nothing else has to exist: no `[retriever.<name>]` or `[provider.<name>]` block.

```toml
[[instance]]
name = "home"

[[instance.retriever]]
type = "ifconfigco"

[[instance.provider]]
type     = "regru"
zone     = "example.com"
rr_name  = "home"
username = "my-login"
password = "${PASSWORD}"
```

Use it for an object that only one instance needs: most retrievers, and any provider that is not shared across records. The object cannot be referred to from another instance; if a second instance needs it, move it to a definition.

### Reference to a definition

A definition is a `[retriever.<name>]` or `[provider.<name>]` block. `ref` points at it by name, and the instance takes the object as defined. This is how one provider account serves several instances, or one retriever serves several sites.

```toml
[provider.regru]
type     = "regru"
zone     = "example.com"
rr_name  = "home"
username = "my-login"
password = "${PASSWORD}"

[[instance]]
name = "home"

[[instance.provider]]
ref = "regru"
```

When nothing is overridden, the table can be replaced with the name alone: `"regru"` is the same as a table with only `ref = "regru"`. This is the short form, and it is the most compact way to assemble an instance from ready definitions:

```toml
[[instance]]
name      = "home"
retriever = ["ipify"]
provider  = ["regru", "selectel"]
```

### Overriding fields

Parameters written next to `ref` replace the same parameters of the definition for this instance only; everything else is taken from the definition. One provider account then serves several records:

```toml
[[instance]]
name = "home"

[[instance.provider]]
ref     = "regru"
rr_name = "*.home"                 # a different record, the rest comes from [provider.regru]
```

Only the parameters of the plugin can be overridden. `type` cannot: it is fixed by the definition, and `ref` and `type` in one table are an error. A name in the short form cannot carry overrides, so an element with an override is always a table.

### Choosing a form

| You want | Write |
|---|---|
| An object used by one instance only | inline: `[[instance.retriever]]` or `[[instance.provider]]` with `type` |
| A shared object, used as it is | the short form, `provider = ["regru"]` |
| A shared object with a different zone, record or address | `[[instance.provider]]` with `ref` and the changed parameters |

### Mixing forms

The order of the elements is kept; for retrievers it is the polling order. Elements of one list can be of different kinds: an instance may have one retriever by `ref` and another inline.

TOML does not allow one key to be written both as an array of names and as `[[...]]` tables in the same instance, so `provider = ["regru"]` and `[[instance.provider]]` cannot be used together. Different keys can: `retriever = ["ipify"]` goes together with `[[instance.provider]]`. If one provider needs an override, write all providers of that instance as `[[instance.provider]]` tables.

### Notifiers

`notify` works the same way: `notify = ["alerts"]` is the short form, and a `[[instance.notify]]` table may only hold `ref` and `events`. It chooses which [event types](../operations/notification-events.md#choosing-the-events) reach the notifier from this instance, and nothing else about it can be overridden.

```toml
[[instance]]
name = "lab"

[[instance.notify]]
ref    = "alerts"
events = ["status"]
```

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
