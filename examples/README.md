# Configuration examples

Each file is a self-contained `dnspatch.toml`: copy it, replace the
`CHANGE_ME` placeholders and the `${...}` references, and start the daemon
with `--config`. They are parsed in CI (see
[../internal/config/examples_test.go](../internal/config/examples_test.go)),
so the syntax always matches the current release.

| File | Shows |
|------|-------|
| [minimal.toml](minimal.toml) | The smallest working config: one retriever, one provider, one instance. Start here. |
| [dual-stack.toml](dual-stack.toml) | One instance writing both an A and an AAAA record, using two retrievers pinned to `ipv4` and `ipv6`. |
| [family-dual-with-fallback.toml](family-dual-with-fallback.toml) | A retriever whose service is itself dual-stack (`family = "dual"`), plus a fallback chain across further retrievers for the families it does not fill. |
| [proxy.toml](proxy.toml) | Reaching a provider's API through a proxy, for a DNS host that only accepts requests from a fixed address. |
| [secrets-from-files.toml](secrets-from-files.toml) | Reading a secret from a file (`${file:/path}`) instead of an environment variable, the way Docker/Kubernetes secrets are mounted. |
| [multi-provider.toml](multi-provider.toml) | Several instances and providers in one process: two sites, two DNS hosts, independent polling intervals. |

For the full parameter reference of every plugin see
[../docs/PARAMETERS.md](../docs/PARAMETERS.md). The general syntax
(`${NAME}` expansion, `ref` overrides, proxies) is documented in the
[repository README](../README.md#configuration).
