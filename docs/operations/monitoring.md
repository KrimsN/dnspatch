# Monitoring

Three mechanisms, from the inside out: a health check of the process itself, a ping to an external monitor on every cycle, and notifications about what an instance does.

## Health check

The daemon writes a status file per instance on every completed cycle (successful or not: a failing provider is still activity, already reported through logging and backoff), by default under `$TMPDIR/dnspatch-health` (`DNSPATCH_HEALTH_DIR` overrides it).

`dnspatch healthcheck` re-reads the config to learn each instance's own interval, checks that every status file is fresh (at most twice the instance's interval old, at least 30s), and exits non-zero otherwise. It needs no shell or curl, which a distroless image does not have. Both the lightweight and the full images already run it as their `HEALTHCHECK`.

If the container's filesystem is `read_only`, mount `/tmp` (or wherever `DNSPATCH_HEALTH_DIR` points) as `tmpfs`, as [compose.yml](https://github.com/dnspatch/dnspatch/blob/main/compose.yml) does; otherwise every write fails, and `healthcheck` reports the daemon as stuck even though it is working fine. A write failure never affects the DNS updates themselves, only the health check.

## Monitoring pings

`ping_url`, set on an instance, is called on every completed cycle: a GET request on success, and the same URL with `/fail` appended on failure. It is compatible with [Healthchecks.io](https://healthchecks.io) and [Uptime Kuma](https://github.com/louislam/uptime-kuma) push monitors. Unlike the health check above, this reaches an external service: it works as a dead man's switch, alerting when the ping stops arriving even if dnspatch's own process and container stay up.

```toml
[[instance]]
name     = "home"
ping_url = "${PING_URL}"   # for example https://hc-ping.com/<uuid>
# ...
```

`ping_url` only works on a build with the `ping` tag (the full binary or image); the lightweight build rejects a config that sets it, rather than silently ignoring it, since the field would otherwise do nothing without any indication why.

## Notifications

A `[notify.<name>]` definition describes a message broker, and an instance that publishes to it sends events to it. By default that is one kind of event, a flip of the instance's status between success and failure; the `events` key chooses others (see [Event types](#event-types)). The default is deliberately quiet, since a notification channel is for what deserves a human's attention, unlike the ping above, which needs a heartbeat on every cycle to work as a dead man's switch.

```toml
[notify.alerts]
type    = "redis"
address = "${REDIS_URL}"   # a redis:// URL; carries auth and the database index
events  = ["status", "ip_change"]   # optional; the default is ["status"]
```

### Connecting instances to notifiers

Notifiers are defined the way retrievers and providers are, and each instance names the ones it publishes to in its `notify` list. An instance with no `notify` key publishes to every notifier the file defines, and `notify = []` gives it none. Each definition is its own broker connection, shared by the instances that list it, with its own optional `topic_prefix`, so several can be of one type:

```toml
[notify.alerts]
type    = "redis"
address = "${REDIS_URL}"

[notify.backup]
type         = "redis"
address      = "${REDIS_URL_BACKUP}"
topic_prefix = "backup.dnspatch."

[[instance]]
name   = "home"
notify = ["alerts"]             # only alerts

[[instance]]
name   = "office"
notify = ["alerts", "backup"]

[[instance]]
name = "lab"                    # no notify key: alerts and backup
```

### Event types

This is a summary. Every type, with its fields, an example message and the situations in which it arrives, is described on [Notification events](notification-events.md).

`events` is a list of the types a notifier publishes. It is a setting of dnspatch, not of the broker, so it works the same for every notifier. Without it a notifier publishes `["status"]`, as it did before event types existed.

| Type | What it reports | When |
|---|---|---|
| [`status`](notification-events.md#status) | the instance as a whole worked or failed | when its state changes |
| [`provider_status`](notification-events.md#provider_status) | one provider worked or failed | when its state changes |
| [`retriever_status`](notification-events.md#retriever_status) | one retriever worked or failed | when its state changes |
| [`ip_change`](notification-events.md#ip_change) | an address was written to a provider and differs from the previous one | once per cycle, with every change of that cycle |
| [`cycle`](notification-events.md#cycle) | a cycle finished, successfully or not | after every cycle |
| [`lifecycle`](notification-events.md#lifecycle) | the instance started or stopped | at start, before the first cycle, and at a normal stop |

The three `*_status` types report transitions only. The first time something is seen it is reported only if it failed: a first success is not news. A provider's state changes only when dnspatch really tried to write: a cycle that skipped it because the address had not changed, or because it is waiting out a backoff, leaves it alone. In the same way a retriever's state changes only when it was called: one that is not needed because an earlier retriever already supplied the address keeps what it had, even if that was a failure.

`lifecycle` is published by every instance to its own topic, so a daemon with three instances sends three `started` events. The `stopped` event is delivered before the connection to the broker is closed.

!!! note
    While a provider is waiting out a backoff, dnspatch counts the cycle as successful, so `status` (and `ping_url`) flip between failure and recovery during that time. To follow a provider precisely, use `provider_status`.

### Choosing events per instance

An entry of an instance's `notify` list is either the name of a definition, which brings the definition's `events`, or a table with a `ref` and its own `events`, which replaces them for this instance only. Names and tables can be mixed in an inline array, and the form with `[[instance.notify]]` headers works too; TOML does not allow both for one instance.

```toml
[notify.alerts]
type   = "redis"
events = ["status", "provider_status", "retriever_status", "ip_change"]

[notify.audit]
type    = "rabbitmq"
address = "${AMQP_URL}"
events  = ["cycle", "lifecycle"]

[[instance]]
name   = "home"
notify = ["alerts", "audit"]        # the events of the definitions

[[instance]]
name   = "lab"
notify = ["audit", { ref = "alerts", events = ["status"] }]

[[instance]]
name = "office"

[[instance.notify]]
ref    = "alerts"
events = ["status"]                 # the same override, as a table

[[instance.notify]]
ref = "audit"                       # no events: the definition's
```

Only `ref` and `events` are allowed in such a table. The connection to the broker belongs to the definition and is shared by every instance that uses it, so an instance cannot change its address or prefix; define another notifier for that. `events` must not be empty and must not repeat a type; to publish nothing to a notifier, leave it out of the list. A mistake is reported at startup with the instance or definition it is in, and `dnspatch --check-config` shows the events each notifier gets in each instance, for example `notify=[alerts(status), audit(cycle, lifecycle)]`.

### What is published

dnspatch itself never talks to Telegram, Slack or anything else: it publishes a small JSON event to the channel `dnspatch.events.<instance>` (override the prefix with `topic_prefix`), and whatever is subscribed to it, a bot you write or a small relay service, decides what to do next. This keeps adding a new notification channel a change on the listener's side only, with dnspatch's config and binary untouched. The topic is the same for every type of event; tell them apart by the `event` field.

Every message is a JSON object with `event`, `severity`, `instance` and `time`, plus the fields of its type:

```json
{"event":"status","severity":"error","instance":"home","time":"2026-10-02T10:00:03+07:00","state":"failure","success":false,"error":"provider \"regru\": ..."}
```

The fields of each type, when it is sent, the severity and typical sequences of events are on [Notification events](notification-events.md). The payload of 0.4.0 (`instance`, `success`, `error`, `time`) is inside `status`, so a consumer written for it keeps working; `event`, `severity` and `state` are added to it.

The text of an error is the same as in dnspatch's log, and the same care is needed with it: a plugin must keep secrets, such as a token in a URL, out of its errors.

### Redis

The `redis` notifier takes a `redis://` URL as `address`. Redis Pub/Sub is fire-and-forget: a subscriber that is not connected when an event is published misses it, which is fine for state changes, since the next one (or the next `ping_url` or health check cycle) still gets through; with `cycle` or `lifecycle` a missed event is simply gone.

### RabbitMQ

The `rabbitmq` notifier (`address` is an `amqp://` or `amqps://` URL) publishes to a durable topic exchange, `dnspatch` by default (`exchange` changes it), with `dnspatch.events.<instance>` as the routing key. Bind a queue to the exchange with the pattern you want (`dnspatch.events.#` for everything) and events wait there while the consumer is away; with no queue bound, the broker drops them. The connection is opened on the first event, not at startup, and re-opened after a failure.

### Build requirements

Like `ping_url`, a notifier needs a build that has its backend compiled in: the `redis` or `rabbitmq` tag for these, or `notify_all` for every backend (the full binary and image use it); see [Building from source](../deployment/building.md). The lightweight build rejects a config whose instances use a notifier; a definition that no instance uses is ignored, and `dnspatch --check-config` shows the notifiers each instance publishes to. A build that lacks the backend a definition names says which tag brings it, and an error in one definition is reported by its name (`notify "backup" (redis): ...`).

Adding another backend (MQTT, ...) is a `plugins/notifiers/<backend>` package that implements `plugin.Notifier` and registers itself in `init`, like a provider does; `go generate` gives it a build tag. See [Writing a plugin](../development/writing-a-plugin.md).
