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

### Inline, reference and short form

=== "Inline"

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

=== "Reference"

    A definition is a `[retriever.<name>]` or `[provider.<name>]` block. `ref` points at it by name, and the instance takes the object as defined. This is how one provider account serves several instances, or one retriever serves several sites.

    ```toml
    [retriever.home]
    type = "ifconfigco"

    [provider.regru]
    type     = "regru"
    zone     = "example.com"
    rr_name  = "home"
    username = "my-login"
    password = "${PASSWORD}"

    [[instance]]
    name = "home"

    [[instance.retriever]]
    ref = "home"

    [[instance.provider]]
    ref = "regru"

    [[instance.provider]]
    ref     = "regru"
    rr_name = "*.home"             # override: another record on the same account

    [[instance]]
    name = "office"

    [[instance.retriever]]
    ref      = "home"
    base_url = "https://10.0.0.1"  # override: the same retriever with another address

    [[instance.provider]]
    ref     = "regru"
    rr_name = "office"             # override: the rest comes from [provider.regru]
    ```

    Parameters written next to `ref` replace the same parameters of the definition for this instance only; see [Overriding fields](#overriding-fields).

=== "Short form"

    When nothing is overridden, the table can be replaced with the name alone: `"regru"` is the same as a table with only `ref = "regru"`. This is the most compact way to assemble an instance from ready definitions.

    ```toml
    [retriever.home]
    type = "ifconfigco"

    [provider.regru]
    type     = "regru"
    zone     = "example.com"
    rr_name  = "home"
    username = "my-login"
    password = "${PASSWORD}"

    [provider.selectel]
    type         = "selectel"
    zone         = "example.net"
    rr_name      = "home"
    project_name = "my-project"
    account_id   = "123456"
    username     = "my-login"
    password     = "${SELECTEL_PASSWORD}"

    [[instance]]
    name      = "home"
    retriever = ["home"]
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

TOML does not allow one key to be written both as an array of names and as `[[...]]` tables in the same instance, so `provider = ["regru"]` and `[[instance.provider]]` cannot be used together. Different keys can: `retriever = ["home"]` goes together with `[[instance.provider]]`. If one provider needs an override, write all providers of that instance as `[[instance.provider]]` tables.

### Notifiers

`notify` takes the same three forms. A referenced notifier is shared: the connection to the broker belongs to the definition and is used by every instance that lists it. That is why a `[[instance.notify]]` table with `ref` may hold only `ref` and `events`; `events` chooses which [event types](../operations/notification-events.md#choosing-the-events) reach the notifier from this instance, and nothing else about it can be overridden.

=== "Inline"

    A notifier used by one instance only is declared in place, with `type` instead of `ref`. The table takes every parameter of the plugin, plus `events`.

    ```toml
    [[instance]]
    name = "lab"

    [[instance.notify]]
    type    = "redis"
    address = "${REDIS_URL}"
    events  = ["status", "ip_change"]
    ```

    Such a notifier has a connection of its own and is named `<instance>/<type>#<position>` in `--check-config` and in logs, here `lab/redis#1`. Declared identically in several instances it makes several connections; when that matters, move it to `[notify.<name>]` and refer to it. A notifier declared in place belongs to its instance: an instance without `notify` publishes only to the `[notify.<name>]` definitions.

=== "Reference"

    The definition is a `[notify.<name>]` block; `ref` points at it. `events` is optional and replaces the definition's events for this instance only.

    ```toml
    [notify.alerts]
    type    = "redis"
    address = "${REDIS_URL}"
    events  = ["status", "ip_change"]

    [[instance]]
    name = "lab"

    [[instance.notify]]
    ref    = "alerts"
    events = ["status"]          # only this instance; alerts itself keeps its two events
    ```

=== "Short form"

    The name alone, with the events of the definition. An instance can list several notifiers.

    ```toml
    [notify.alerts]
    type    = "redis"
    address = "${REDIS_URL}"
    events  = ["status", "ip_change"]

    [notify.audit]
    type    = "rabbitmq"
    address = "${AMQP_URL}"
    events  = ["cycle", "lifecycle"]

    [[instance]]
    name   = "lab"
    notify = ["alerts", "audit"]
    ```

    Without a `notify` key an instance publishes to every `[notify.<name>]` of the file, and `notify = []` gives it none.

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
