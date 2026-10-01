# Monitoring

Three mechanisms, from the inside out: a health check of the process itself, a ping to an external monitor on every cycle, and notifications when an instance's status flips.

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

A `[notify.<name>]` definition describes a message broker, and an instance that publishes to it sends an event whenever the instance's status flips between success and failure. Events are not sent on every cycle, since that would just be noise for a notification channel (unlike the ping above, which needs a heartbeat on every cycle to work as a dead man's switch).

```toml
[notify.alerts]
type    = "redis"
address = "${REDIS_URL}"   # a redis:// URL; carries auth and the database index
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

### What is published

dnspatch itself never talks to Telegram, Slack or anything else: it publishes a small JSON event to the channel `dnspatch.events.<instance>` (override the prefix with `topic_prefix`), and whatever is subscribed to it, a bot you write or a small relay service, decides what to do next. This keeps adding a new notification channel a change on the listener's side only, with dnspatch's config and binary untouched.

```json
{"instance": "home", "success": false, "error": "...", "time": "..."}
```

### Redis

The `redis` notifier takes a `redis://` URL as `address`. Redis Pub/Sub is fire-and-forget: a subscriber that is not connected when an event is published misses it, which is fine here since the next status change (or the next `ping_url` or health check cycle) still gets through.

### RabbitMQ

The `rabbitmq` notifier (`address` is an `amqp://` or `amqps://` URL) publishes to a durable topic exchange, `dnspatch` by default (`exchange` changes it), with `dnspatch.events.<instance>` as the routing key. Bind a queue to the exchange with the pattern you want (`dnspatch.events.#` for everything) and events wait there while the consumer is away; with no queue bound, the broker drops them. The connection is opened on the first event, not at startup, and re-opened after a failure.

### Build requirements

Like `ping_url`, a notifier needs a build that has its backend compiled in: the `redis` or `rabbitmq` tag for these, or `notify_all` for every backend (the full binary and image use it); see [Building from source](../deployment/building.md). The lightweight build rejects a config whose instances use a notifier; a definition that no instance uses is ignored, and `dnspatch --check-config` shows the notifiers each instance publishes to. A build that lacks the backend a definition names says which tag brings it, and an error in one definition is reported by its name (`notify "backup" (redis): ...`).

Adding another backend (MQTT, ...) is a `plugins/notifiers/<backend>` package that implements `plugin.Notifier` and registers itself in `init`, like a provider does; `go generate` gives it a build tag. See [Writing a plugin](../development/writing-a-plugin.md).
