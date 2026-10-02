# Notification events

The reference for what a [notifier](monitoring.md#notifications) publishes: every type of event, how to switch it on, when it arrives, and the exact fields of its message. The setup of brokers and the connection of instances to them is on the [Monitoring](monitoring.md#notifications) page.

## Choosing events

Which events reach a broker is decided by `events`, a list of the names below. It is a setting of dnspatch and works the same for every notifier (`redis`, `rabbitmq`, ...).

| Where | Syntax | Effect |
|---|---|---|
| In `[notify.<name>]` | `events = ["status", "ip_change"]` | The events of every instance that uses this notifier |
| Not set | | `["status"]`, the behaviour before event types existed |
| In an instance's `notify` list | `{ ref = "alerts", events = ["cycle"] }` | Replaces the notifier's events for this instance only |
| In an instance's `notify` list | `"alerts"` | The notifier's own events |

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
notify = ["alerts", "audit"]                               # each with its own events

[[instance]]
name   = "lab"
notify = ["audit", { ref = "alerts", events = ["status"] }] # alerts: only status, here
```

The valid names are `status`, `provider_status`, `retriever_status`, `ip_change`, `cycle` and `lifecycle`. `events` must not be empty and must not repeat a name; an unknown name is an error that lists the valid ones. Only `ref` and `events` are allowed in a table of an instance's `notify`, because the connection to the broker belongs to the notifier and is shared by every instance that uses it. See [Choosing events per instance](monitoring.md#choosing-events-per-instance) for the TOML forms, and run `dnspatch --check-config` to see the events each instance ends up with.

## Delivery

- Every event goes to the topic `<topic_prefix><instance>` (by default `dnspatch.events.<instance>`). The topic is the same for all types of one instance; tell them apart by the `event` field.
- The message body is one JSON object.
- Events of one instance are published in the order they happen. Within one cycle that order is: `retriever_status`, `provider_status` (providers in the order of the instance), `ip_change`, `status`, `cycle`.
- A publication is cut off after 5 seconds. If it fails, dnspatch logs a warning and carries on: a broker problem never stops the DNS updates, and the event is not retried.
- A cycle that is interrupted by a shutdown reports nothing, since its result is not known.
- dnspatch keeps what it needs for transitions in memory only. After a restart it starts from scratch: an outage that is still going on is reported as a new failure, and every provider gets an `ip_change` with an empty `old`.

## Fields of every event

| Field | Type | Meaning |
|---|---|---|
| `event` | string | The type: `status`, `provider_status`, `retriever_status`, `ip_change`, `cycle` or `lifecycle` |
| `severity` | string | `info`, `warning` or `error`; fixed for each event, see [Severity](#severity) |
| `instance` | string | The name of the instance |
| `time` | string | When it happened, RFC 3339 with the daemon's own UTC offset |

## status

**Enable with:** `events = ["status"]`, also the default.

The instance as a whole: did its last cycle work. This is the only event of 0.4.0, and its message still has the fields of that version (`instance`, `success`, `error`, `time`) with `event`, `severity` and `state` added.

**When it arrives:** when the outcome of a cycle differs from the previous one.

- The first cycle after a start is reported only if it failed. A first success is the normal start of the day and is not news.
- Then every change is reported: `failure` when a cycle that worked is followed by one that did not, `recovery` when it is the other way round. Cycles that repeat the previous outcome send nothing.

A cycle fails when any retriever that was called, or any provider that was written to, returned an error. That includes a retriever that failed while a fallback retriever supplied the address. A provider that is skipped because the address did not change, or because it is waiting out a backoff, does not fail the cycle.

!!! warning "Flapping during a backoff"
    A provider that failed is not tried on every cycle but after a growing pause. The cycles in between count as successful, so `status` alternates between `failure` (the cycle that retries) and `recovery` (the cycles that skip it) for as long as the provider is down. This is a known problem. To follow a provider reliably use [`provider_status`](#provider_status).

| Field | Type | Meaning |
|---|---|---|
| `state` | string | `failure` or `recovery` |
| `success` | bool | `false` for a failure, `true` for a recovery |
| `error` | string | Only for a failure: the errors of the cycle, one per failed retriever or provider, joined, for example `provider "regru": status 500` |

```json
{"event":"status","severity":"error","instance":"home","time":"2026-10-02T10:00:03+07:00","state":"failure","success":false,"error":"provider \"regru\": update failed: status 500"}
{"event":"status","severity":"info","instance":"home","time":"2026-10-02T10:10:02+07:00","state":"recovery","success":true}
```

## provider_status

**Enable with:** `events = ["provider_status"]`.

One provider: did its last attempt to write the address work. Use it when one instance updates several providers and you need to know which of them is in trouble.

**When it arrives:** when the result of a real write attempt differs from the previous one. As with `status`, a first observation counts only if it is a failure.

- A write that fails: `failure`, if the provider worked before or had not been tried yet.
- A write that works after a failure: `recovery`. Because a failed provider is retried after a pause, the recovery comes with the first retry that succeeds, not at the moment the provider is back.
- Nothing is sent for a cycle in which the provider was not written to: the address had not changed, or it is waiting out a backoff. Repeated failures of a provider that is already `failed` send nothing either.

| Field | Type | Meaning |
|---|---|---|
| `provider` | string | The name of the provider as in the instance: the `ref` of the definition |
| `state` | string | `failure` or `recovery` |
| `success` | bool | `false` for a failure, `true` for a recovery |
| `error` | string | Only for a failure: the error of the provider |

```json
{"event":"provider_status","severity":"error","instance":"home","time":"2026-10-02T10:00:03+07:00","provider":"regru","state":"failure","success":false,"error":"update failed: status 500"}
{"event":"provider_status","severity":"info","instance":"home","time":"2026-10-02T10:10:02+07:00","provider":"regru","state":"recovery","success":true}
```

## retriever_status

**Enable with:** `events = ["retriever_status"]`.

One retriever: did its last call work. Use it to notice that a source of the address (an external service, a network interface) went bad even though a fallback retriever keeps the instance going.

**When it arrives:** when the result of a call to the retriever differs from the previous one; a first observation counts only if it is a failure.

- A retriever counts as failed when it returns an error, runs out of time, or returns no valid address.
- A retriever is called only while an address family is still missing. One that is not needed in a cycle, because an earlier retriever already supplied everything, is not called and keeps its state: if it had failed, it stays failed, and no `recovery` comes until it is called again and works.

| Field | Type | Meaning |
|---|---|---|
| `retriever` | string | The name of the retriever as in the instance: the `ref` of the definition |
| `state` | string | `failure` or `recovery` |
| `success` | bool | `false` for a failure, `true` for a recovery |
| `error` | string | Only for a failure: the error of the retriever |

```json
{"event":"retriever_status","severity":"warning","instance":"home","time":"2026-10-02T10:00:01+07:00","retriever":"ifconfig","state":"failure","success":false,"error":"attempt timed out after 30s: context deadline exceeded"}
{"event":"retriever_status","severity":"info","instance":"home","time":"2026-10-02T10:05:01+07:00","retriever":"ifconfig","state":"recovery","success":true}
```

## ip_change

**Enable with:** `events = ["ip_change"]`.

An address was written to a provider and it differs from the one written before. This is the event to listen to when you want to know that the address of your home or office changed.

**When it arrives:** once per cycle, if at least one provider was written to successfully in that cycle. `changes` lists every successful write of the cycle, for every provider and every address family. A write that failed is not in the list, and a cycle in which nothing was written (the address did not change, or all providers failed) sends nothing.

`old` is empty when dnspatch does not know the previous address. That is the case for the first write after a start, since nothing is remembered between runs, and for the first write after a failed one, since the content of the record is then uncertain. So every start of the daemon sends an `ip_change` for each provider, even if the address is the same as before the restart.

| Field | Type | Meaning |
|---|---|---|
| `changes` | array | One element per address written |
| `changes[].provider` | string | The provider the address was written to |
| `changes[].family` | string | `ipv4` or `ipv6` |
| `changes[].old` | string | The previous address, or an empty string if unknown |
| `changes[].new` | string | The address that was written |

```json
{"event":"ip_change","severity":"info","instance":"home","time":"2026-10-02T10:05:02+07:00","changes":[{"provider":"regru","family":"ipv4","old":"1.2.3.4","new":"5.6.7.8"},{"provider":"cloudflare","family":"ipv4","old":"1.2.3.4","new":"5.6.7.8"}]}
{"event":"ip_change","severity":"info","instance":"home","time":"2026-10-02T09:00:02+07:00","changes":[{"provider":"regru","family":"ipv4","old":"","new":"5.6.7.8"},{"provider":"regru","family":"ipv6","old":"","new":"2001:db8::1"}]}
```

## cycle

**Enable with:** `events = ["cycle"]`.

A cycle (one pass of the instance over its retrievers and providers) finished. It is a heartbeat with a result, for an audit log or a metrics pipeline.

**When it arrives:** after every cycle, whether it succeeded or failed, and also when nothing was written because the address had not changed. With an `interval` of 5 minutes that is 288 messages a day per instance, so enable it for a notifier that is meant for a log, not for a chat. A cycle interrupted by a shutdown sends nothing.

| Field | Type | Meaning |
|---|---|---|
| `success` | bool | Whether the cycle worked, by the same rule as [`status`](#status) |
| `error` | string | Only for a failed cycle: the joined errors, as in `status` |

```json
{"event":"cycle","severity":"info","instance":"home","time":"2026-10-02T10:05:03+07:00","success":true}
{"event":"cycle","severity":"error","instance":"home","time":"2026-10-02T10:10:03+07:00","success":false,"error":"retriever \"ifconfig\": attempt timed out after 30s: context deadline exceeded"}
```

Unlike `status`, `cycle` has no `state`: it reports every cycle, not changes.

## lifecycle

**Enable with:** `events = ["lifecycle"]`.

An instance started or stopped. One event type with two meanings, told apart by `state`.

**When it arrives:**

- `started`: when the instance begins, before its first cycle.
- `stopped`: when the instance ends at a normal shutdown (SIGINT or SIGTERM, for example `docker stop`), after its last cycle. It is published before the connection to the broker is closed, within 5 seconds.

Every instance sends its own pair to its own topic; there is no event for the daemon as a whole. A daemon with three instances sends three `started`. A process that is killed (`kill -9`, power loss, an out-of-memory kill) sends no `stopped`, so the absence of one after a `started` means the instance did not stop normally. If the process crashes and is restarted, a new `started` arrives.

| Field | Type | Meaning |
|---|---|---|
| `state` | string | `started` or `stopped` |
| `version` | string | The version of dnspatch |

```json
{"event":"lifecycle","severity":"info","instance":"home","time":"2026-10-02T09:00:00+07:00","state":"started","version":"0.5.0"}
{"event":"lifecycle","severity":"info","instance":"home","time":"2026-10-02T18:30:12+07:00","state":"stopped","version":"0.5.0"}
```

## Severity

Fixed for each event; it only tells the receiver how to treat the message and cannot be configured or used as a filter.

| Event | `severity` |
|---|---|
| `status`, `failure` | `error` |
| `provider_status`, `failure` | `error` |
| `retriever_status`, `failure` | `warning`: a fallback retriever may still serve the cycle, and if none does, the cycle itself reports an `error` |
| `status`, `provider_status`, `retriever_status`, `recovery` | `info` |
| `ip_change` | `info` |
| `cycle` | `info` for a success, `error` for a failure |
| `lifecycle` | `info` |

## Scenarios

What arrives in common situations. The instance `home` has one retriever `ifconfig`, one provider `regru` and an interval of 5 minutes, and a notifier with every event enabled.

**A normal start.**

1. `lifecycle` `started`
2. `ip_change` with `old` empty (the first write of the provider)
3. `cycle` with `success: true`

No `status` or `provider_status`: first successes are not news. Cycles after that send only `cycle`, until something changes.

**The address changes.** The next cycle sends `ip_change` with `old` and `new`, then `cycle`.

**The provider's API is down.**

1. At the first failed write: `provider_status` `failure`, `status` `failure` and `cycle` with `success: false`.
2. During the pause before the next attempt the provider is skipped, and the cycle counts as successful: `status` `recovery` (this is the [flapping](#status) described above) and `cycle` with `success: true`.
3. Further attempts that fail send `status` `failure` and `cycle` again, but no new `provider_status`: the provider is already failed.
4. When a retry works: `provider_status` `recovery`, then `ip_change` with `old` empty, `status` `recovery` (if it was failed) and `cycle`.

**The primary retriever fails and a fallback serves the address.** `retriever_status` `failure` (severity `warning`) for the primary, then, as the cycle reports the error, `status` `failure` and `cycle` with `success: false`. The address is still written from the fallback, so `ip_change` comes too if it changed. When the primary works again: `retriever_status` `recovery`.

**Every retriever fails.** `retriever_status` `failure` for each of them, then `status` `failure` and `cycle` with `success: false`. No provider is touched, so there is no `provider_status`.

**A planned stop.** `lifecycle` `stopped` for every instance, then the connection to the broker closes. After `docker start` the instance sends `started` and an `ip_change` with `old` empty again.
