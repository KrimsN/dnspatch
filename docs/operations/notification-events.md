# Notification event types

A [notifier](monitoring.md#notifications) publishes events: small JSON messages about what an instance is doing. This page is the reference for them: every type of event, how to switch it on, when it arrives and when it does not, and the exact fields of its message. How brokers are defined and connected to instances is on the [Monitoring](monitoring.md#notifications) page.

## The types at a glance

<div class="grid cards" markdown>

-   **[`status`](#status)**

    ---

    The instance as a whole started failing or recovered.

    *Arrives when the outcome of a cycle changes. On by default.*

-   **[`provider_status`](#provider_status)**

    ---

    One provider started failing or recovered.

    *Arrives when the result of a write to it changes.*

-   **[`retriever_status`](#retriever_status)**

    ---

    One retriever started failing or recovered.

    *Arrives when the result of a call to it changes.*

-   **[`ip_change`](#ip_change)**

    ---

    An address was written to a provider and differs from the previous one.

    *Arrives once per cycle that wrote something.*

-   **[`cycle`](#cycle)**

    ---

    A cycle finished, successfully or not.

    *Arrives after every cycle.*

-   **[`lifecycle`](#lifecycle)**

    ---

    An instance started or stopped.

    *Arrives at start and at a normal stop.*

</div>

The three `*_status` types are **transitions**: they say that something changed, not how it is now, and a thing that stays broken is reported once. `ip_change`, `cycle` and `lifecycle` are **occurrences**: each one is a fact that happened.

## Choosing the events

Which events reach a broker is decided by `events`, a list of the names above. It is a setting of dnspatch, so it works the same for every notifier (`redis`, `rabbitmq`, `mqtt`, ...), and a notifier plugin does not know about it.

There are two places to set it, the notifier and the instance that uses it. Without `events` anywhere a notifier publishes `["status"]`, the behaviour before event types existed. The `events` of the instance's `[[instance.notify]]` take priority over those of the notifier.

=== "In the notifier"

    Set the events once in `[notify.<name>]`; every instance that uses the notifier gets them.

    ```toml
    [notify.alerts]
    type   = "redis"
    events = ["status", "provider_status", "retriever_status", "ip_change"]

    [notify.audit]
    type    = "rabbitmq"
    address = "${AMQP_URL}"
    events  = ["cycle", "lifecycle"]

    [[instance]]
    name = "home"
    # no notify key: the instance publishes to every notifier above,
    # alerts with its four events and audit with cycle and lifecycle

    [[instance]]
    name   = "quiet"
    notify = []   # an empty list: this instance publishes to no notifier
    ```

=== "In the instance"

    Add an `[[instance.notify]]` table for each notifier the instance publishes to. `ref` is the name of the notifier. `events` replaces the notifier's events for this instance only; without it the instance gets the notifier's own.

    ```toml
    [notify.alerts]
    type   = "redis"
    events = ["status", "provider_status", "retriever_status", "ip_change"]

    [notify.audit]
    type    = "rabbitmq"
    address = "${AMQP_URL}"
    events  = ["cycle", "lifecycle"]

    [[instance]]
    name = "lab"

    [[instance.notify]]
    ref = "audit"                # no events: cycle and lifecycle, from the notifier

    [[instance.notify]]
    ref    = "alerts"
    events = ["status"]          # only status, in this instance
    ```

=== "Short form"

    When an instance takes the events of its notifiers as they are, a list of their names is enough.

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
    notify = ["alerts", "audit"]     # each with the events of its notifier
    ```

In one instance `notify` is written either as a list of names or as `[[instance.notify]]` tables, not both. An instance without a `notify` key publishes to every notifier the file defines, and `notify = []` gives it none.

The rules, each reported at startup with the instance or notifier it is in:

- The valid names are `status`, `provider_status`, `retriever_status`, `ip_change`, `cycle` and `lifecycle`. An unknown name is an error that lists them.
- `events` must not be empty, and must not repeat a name. To publish nothing to a notifier, leave it out of the instance's `notify`.
- A `[[instance.notify]]` table with `ref` takes only `ref` and `events`. The connection to the broker belongs to the notifier and is shared by every instance that uses it, so an instance cannot change its address or prefix. To configure it for one instance, declare a notifier in place with `type` instead of `ref`; `ref` and `type` in one table are an error.

`dnspatch --check-config` prints the events each instance ends up with, for example `notify=[alerts(status), audit(cycle, lifecycle)]`.

## Delivery

- Every event goes to the topic `<topic_prefix><instance>`, by default `dnspatch.events.<instance>`. The topic is the same for all types of one instance; tell them apart by the `event` field.
- The message body is one JSON object.
- Events of one instance are published in the order they happen. Within one cycle that order is: `retriever_status`, `provider_status` (providers in the order of the instance), `ip_change`, `status`, `cycle`.
- A publication is cut off after 5 seconds. If it fails, dnspatch logs a warning and goes on: a broker problem never stops the DNS updates, and the event is not retried. Redis Pub/Sub also drops an event that nobody is subscribed to at that moment.
- A cycle that a shutdown interrupts reports nothing, since its result is not known.
- What dnspatch needs for transitions is kept in memory only. After a restart it starts from scratch: an outage that is still going on is reported again as a new failure, and every provider gets an `ip_change` with an empty `old`.

## Fields of every event

| Field | Type | Meaning |
|---|---|---|
| `event` | string | The type: `status`, `provider_status`, `retriever_status`, `ip_change`, `cycle` or `lifecycle` |
| `severity` | string | `info`, `warning` or `error`; fixed for each event, see [Severity](#severity) |
| `instance` | string | The name of the instance |
| `time` | string | When it happened, RFC 3339, with the UTC offset of the machine dnspatch runs on |

The text in `error` is the same as in dnspatch's log. When several things failed in one cycle, the messages are joined with a line break. A plugin is responsible for keeping secrets, such as a token in a URL, out of its errors.

## status

The instance as a whole: did its last cycle work. This was the only event of 0.4.0, and its message still has the fields of that version (`instance`, `success`, `error`, `time`), with `event`, `severity` and `state` added to them.

**Switch on:** `events = ["status"]`, which is also the default. **Use it for** a single alert per instance: "something is wrong with `home`" and "it is fine again".

=== "When it arrives"

    **Arrives**

    - When the outcome of a cycle differs from that of the previous one: `failure` after a cycle that worked, `recovery` after a cycle that failed.
    - For the first cycle after a start, only if it failed. A first success is the normal start of the day and is not news.

    **Does not arrive**

    - For a cycle that repeats the outcome of the previous one: a long outage is one `failure`, not one per cycle.
    - For a cycle that a shutdown interrupted.

    **What fails a cycle.** A cycle fails when any retriever that was called or any provider that was written to returned an error. That includes a retriever that failed while a fallback retriever supplied the address. A provider that is skipped because its address did not change does not fail the cycle. A provider that is waiting out a backoff does: the cycle reports the error of its last failed write, so it stays failed until the provider is written again.

=== "Message"

    | Field | Type | Meaning |
    |---|---|---|
    | `state` | string | `failure` or `recovery` |
    | `success` | bool | `false` for a failure, `true` for a recovery |
    | `error` | string | Only for a failure: the errors of the cycle, one per failed retriever or provider |

    ```json
    {
      "event": "status",
      "severity": "error",  // (1)!
      "instance": "home",
      "time": "2026-10-02T10:00:03+07:00",
      "state": "failure",  // (2)!
      "success": false,
      "error": "provider \"regru\": update failed: status 500"  // (3)!
    }
    ```

    1. `error` for a failure, `info` for a recovery.
    2. `failure` when a cycle that worked is followed by one that did not; `recovery` the other way round.
    3. Present only for a failure. The text is the one in the log; with several failed retrievers or providers the messages are joined with a line break.

    A recovery has no `error`:

    ```json
    {"event":"status","severity":"info","instance":"home","time":"2026-10-02T10:10:02+07:00","state":"recovery","success":true}
    ```

## provider_status

One provider: did its last attempt to write the address work.

**Switch on:** `events = ["provider_status"]`. **Use it for** an instance with several providers, to know which of them is in trouble, and to see which of them is failed.

=== "When it arrives"

    **Arrives**

    - When the result of a real write attempt differs from that of the previous one: `failure` when a write fails after one that worked or the first time the provider is tried, `recovery` when a write works after a failure.
    - A failed provider is retried after a pause that grows with each failure, so `recovery` comes with the first retry that succeeds, not at the moment the provider is back.

    **Does not arrive**

    - For a cycle in which the provider was not written to: its address had not changed, or it is waiting out a backoff.
    - For a retry that fails again: the provider is already failed.
    - For the first write after a start, if it worked.

=== "Message"

    | Field | Type | Meaning |
    |---|---|---|
    | `provider` | string | The name of the provider in the instance: the `ref` of its definition |
    | `state` | string | `failure` or `recovery` |
    | `success` | bool | `false` for a failure, `true` for a recovery |
    | `error` | string | Only for a failure: the error of the provider |

    ```json
    {
      "event": "provider_status",
      "severity": "error",
      "instance": "home",
      "time": "2026-10-02T10:00:03+07:00",
      "provider": "regru",  // (1)!
      "state": "failure",
      "success": false,
      "error": "update failed: status 500"  // (2)!
    }
    ```

    1. The `ref` of the provider's definition, as written in the instance.
    2. The error of the provider itself, without the `provider "regru":` prefix that `status` adds.

    ```json
    {"event":"provider_status","severity":"info","instance":"home","time":"2026-10-02T10:10:02+07:00","provider":"regru","state":"recovery","success":true}
    ```

## retriever_status

One retriever: did its last call work.

**Switch on:** `events = ["retriever_status"]`. **Use it for** noticing that a source of the address, an external service or a network interface, went bad while a fallback retriever keeps the instance going. Without it the failure shows up only in the log and in `status`.

=== "When it arrives"

    **Arrives**

    - When the result of a call to the retriever differs from that of the previous one: `failure` or `recovery`. A first observation counts only if it is a failure.
    - A call counts as failed when the retriever returns an error, runs out of time (30 seconds), or returns no valid address.

    **Does not arrive**

    - For a retriever that was not called. A retriever is called only while an address family is still missing, so one behind a working retriever is not called, and it keeps its state: if it had failed it stays failed, and no `recovery` comes until it is called again and works.
    - For a call that repeats the previous result.

=== "Message"

    | Field | Type | Meaning |
    |---|---|---|
    | `retriever` | string | The name of the retriever in the instance: the `ref` of its definition |
    | `state` | string | `failure` or `recovery` |
    | `success` | bool | `false` for a failure, `true` for a recovery |
    | `error` | string | Only for a failure: the error of the retriever |

    ```json
    {
      "event": "retriever_status",
      "severity": "warning",  // (1)!
      "instance": "home",
      "time": "2026-10-02T10:00:01+07:00",
      "retriever": "ifconfig",  // (2)!
      "state": "failure",
      "success": false,
      "error": "attempt timed out after 30s: context deadline exceeded"  // (3)!
    }
    ```

    1. `warning` for a failure, because a fallback retriever may still serve the cycle; `info` for a recovery.
    2. The `ref` of the retriever's definition, as written in the instance.
    3. The error of the retriever itself.

    ```json
    {"event":"retriever_status","severity":"info","instance":"home","time":"2026-10-02T10:05:01+07:00","retriever":"ifconfig","state":"recovery","success":true}
    ```

## ip_change

An address was written to a provider and it differs from the one written before.

**Switch on:** `events = ["ip_change"]`. **Use it for** learning that the address of a home or an office changed, and for reacting to it: updating a firewall allowlist, telling a user, adding a log line.

=== "When it arrives"

    **Arrives**

    - Once per cycle, if at least one provider was written to successfully in it. `changes` lists every successful write of the cycle, for every provider and every address family, so a change of the address with three providers is one event with three elements.
    - On every start of the daemon, for each provider, with an empty `old`. Nothing is remembered between runs, so the first write is always a change from the unknown, even if the address is the same as before the restart.

    **Does not arrive**

    - For a cycle in which nothing was written: the address did not change, or every write failed.
    - For a provider whose write failed: it is not in `changes`.

    `old` is an empty string when dnspatch does not know the previous address: for the first write after a start, and for the first write after a failed one, when the content of the record is uncertain.

=== "Message"

    | Field | Type | Meaning |
    |---|---|---|
    | `changes` | array | One element per address written |
    | `changes[].provider` | string | The provider the address was written to |
    | `changes[].family` | string | `ipv4` or `ipv6` |
    | `changes[].old` | string | The previous address, or an empty string if it is not known |
    | `changes[].new` | string | The address that was written |

    ```json
    {
      "event": "ip_change",
      "severity": "info",
      "instance": "home",
      "time": "2026-10-02T10:05:02+07:00",
      "changes": [  // (1)!
        {
          "provider": "regru",
          "family": "ipv4",  // (2)!
          "old": "1.2.3.4",  // (3)!
          "new": "5.6.7.8"
        },
        {
          "provider": "cloudflare",
          "family": "ipv4",
          "old": "1.2.3.4",
          "new": "5.6.7.8"
        }
      ]
    }
    ```

    1. One event per cycle with every successful write of that cycle, here two providers.
    2. `ipv4` or `ipv6`. A dual-stack instance has an element for each family.
    3. Empty (`""`) when the previous address is not known: after a start or after a failed write.

## cycle

A cycle, one pass of the instance over its retrievers and providers, finished. It is a heartbeat with a result.

**Switch on:** `events = ["cycle"]`. **Use it for** an audit log, metrics, or a dead man's switch on the receiving side, such as alerting when no `cycle` has arrived for a while. Do not point it at a chat: with an `interval` of 5 minutes it is 288 messages a day for each instance.

=== "When it arrives"

    **Arrives**

    - After every cycle, successful or not, including a cycle in which nothing was written because the address had not changed.

    **Does not arrive**

    - For a cycle that a shutdown interrupted.

    Unlike `status`, `cycle` has no `state`: it reports every cycle, not the changes. It fails by the same rule as `status`, so it keeps `success: false` through a backoff.

=== "Message"

    | Field | Type | Meaning |
    |---|---|---|
    | `success` | bool | Whether the cycle worked |
    | `error` | string | Only for a failed cycle: its errors, as in `status` |

    ```json
    {
      "event": "cycle",
      "severity": "error",  // (1)!
      "instance": "home",
      "time": "2026-10-02T10:10:03+07:00",
      "success": false,
      "error": "retriever \"ifconfig\": attempt timed out after 30s: context deadline exceeded"  // (2)!
    }
    ```

    1. `info` for a successful cycle, `error` for a failed one.
    2. Present only for a failed cycle.

    A successful cycle:

    ```json
    {"event":"cycle","severity":"info","instance":"home","time":"2026-10-02T10:05:03+07:00","success":true}
    ```

## lifecycle

An instance started or stopped. One type with two meanings, told apart by `state`.

**Switch on:** `events = ["lifecycle"]`. **Use it for** seeing restarts and deployments: a `started` you did not expect means the process was restarted, and a `started` without a preceding `stopped` means it died.

=== "When it arrives"

    **Arrives**

    - `started`: when the instance begins, before its first cycle.
    - `stopped`: when the instance ends at a normal shutdown (SIGINT or SIGTERM, for example `docker stop`), after its last cycle. It is published before the connection to the broker is closed, within 5 seconds.
    - For every instance separately, to its own topic. There is no event for the daemon as a whole: a daemon with three instances sends three `started` and three `stopped`.

    **Does not arrive**

    - `stopped` when the process is killed (`kill -9`, power loss, an out-of-memory kill). A crash that is followed by a restart shows up as a `started` with no `stopped` before it.
    - `started` for an instance whose configuration is rejected at startup: the daemon does not start at all.

=== "Message"

    | Field | Type | Meaning |
    |---|---|---|
    | `state` | string | `started` or `stopped` |
    | `version` | string | The version of dnspatch |

    ```json
    {
      "event": "lifecycle",
      "severity": "info",
      "instance": "home",
      "time": "2026-10-02T09:00:00+07:00",
      "state": "started",  // (1)!
      "version": "0.5.0"  // (2)!
    }
    ```

    1. `started` before the first cycle, `stopped` at a normal shutdown.
    2. The version of the dnspatch binary that sent the event.

## Severity

Fixed for each event. It only tells the receiver how to treat the message: it cannot be configured, and there is no filter by it.

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

What arrives in common situations. The instance `home` has one retriever `ifconfig`, one provider `regru` and an interval of 5 minutes, and its notifier has every event switched on.

=== "A normal start"

    1. `lifecycle` `started`
    2. `ip_change` with `old` empty: the first write of the provider
    3. `cycle` with `success: true`

    There is no `status` or `provider_status`: first successes are not news. The cycles after that send only `cycle`, until something changes.

=== "The address changes"

    The next cycle sends `ip_change` with `old` and `new`, then `cycle`.

=== "The provider's API is down"

    1. At the first failed write: `provider_status` `failure`, `status` `failure` and `cycle` with `success: false`.
    2. During the pause before the next attempt the provider is skipped, but the cycle still fails with its last error: only `cycle` with `success: false` arrives, since `status` is already `failure`.
    3. A retry that fails sends `cycle` with `success: false` again, but no new `status` or `provider_status`: both are already failed.
    4. When a retry works: `provider_status` `recovery`, `status` `recovery`, `ip_change` with `old` empty, and `cycle` with `success: true`.

=== "A retriever fails, a fallback serves"

    `retriever_status` `failure` (severity `warning`) for the primary, then, because the cycle reports the error, `status` `failure` and `cycle` with `success: false`. The address is still written from the fallback, so `ip_change` comes too if it changed. When the primary works again: `retriever_status` `recovery`.

=== "Every retriever fails"

    `retriever_status` `failure` for each of them, then `status` `failure` and `cycle` with `success: false`. No provider is touched, so there is no `provider_status`.

=== "A planned stop"

    `lifecycle` `stopped` for every instance, then the connection to the broker closes. After `docker start`, the instance sends `started` and an `ip_change` with `old` empty again.
