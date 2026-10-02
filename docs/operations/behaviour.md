# Behaviour

dnspatch is built around three concepts:

- **Retriever**: reports your current public IP address.
- **Provider**: writes that address to a DNS record.
- **Instance**: ties one or more retrievers to one or more providers and polls on its own interval.

Instances run independently, so several sites or networks can be tracked at once.

## Failure handling

- Providers of an instance are updated independently: one failing provider never stops the others (a stuck one delays the rest of the tick by at most its 30-second deadline).
- A provider is written only when the address differs from the last one it accepted; a failed provider is retried on later ticks, the others are left alone.
- A failing provider is retried with exponential backoff and jitter: the delay is at most the polling interval after the first failure (at least half of it) and its ceiling doubles with every further failure, up to 30 minutes (or the interval, if that is longer).
- While a provider is backing off, the cycles that skip it are failed too, with the error of its last failed write: a cycle is successful only when no provider of the instance is in a failed state. This keeps `ping_url` on `/fail` and `status` notifications on `failure` until the provider is written again.
- Every retrieval and every write has a 30-second deadline.

## State is not persisted

The last written address is kept in memory only. **After a restart the daemon does not know what it wrote before**, so the first tick writes the current address to every provider, even if the record already holds it. A provider that was backing off is tried again immediately.

This costs one API call per provider per restart and is intended: a state file would be lost on every restart of a container without a volume, which is exactly where it would be needed, and would bring a path, permissions and a way to be stale of its own. Providers make the write idempotent, so nothing changes when the record is already correct.
