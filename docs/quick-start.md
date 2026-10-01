# Quick start

Copy [dnspatch.toml.example](https://github.com/dnspatch/dnspatch/blob/main/dnspatch.toml.example) to `dnspatch.toml`, fill it in and start the daemon:

```sh
dnspatch --config dnspatch.toml
```

Without `--config` the daemon uses `$DNSPATCH_CONFIG`, then `./dnspatch.toml`, then `/etc/dnspatch/config.toml`. `dnspatch --version` prints the version.

A first configuration is one retriever, one provider and one instance that ties them together:

```toml
[provider.regru]
type     = "regru"
zone     = "example.com"
rr_name  = "home"
username = "my-login"
password = "${PASSWORD}"     # read from the environment

[[instance]]
name = "home"

[[instance.retriever]]
type = "ifconfigco"

[[instance.provider]]
ref = "regru"
```

The [Configuration overview](configuration/index.md) explains each part, and [Examples](configuration/examples.md) has ready-made files for dual-stack, fallback between retrievers, a proxy, secrets from files and more.

## Check the configuration

`dnspatch --check-config` validates the config and exits without starting the daemon. On success it exits with code 0 and prints a summary of every instance's retrievers and providers; on failure it exits with code 2 and prints the problem. It is useful in a systemd `ExecStartPre` or after hand-editing the file, and it confirms the config was read the way it was written, not just that it parses.

When an instance has several providers of one type, the summary also lists the parameters that tell them apart, for example `regru(zone=example.org, rr_name=office)`. The values of secret parameters such as passwords are never printed: they show up as `***` only when passwords alone tell the providers apart, and are left out whenever anything else differs.

## Logs and exit codes

Logs go to stderr at the `info` level. `--log-level debug` (or `DNSPATCH_LOG_LEVEL=debug`; the flag wins) also shows why a provider was skipped: the address is unchanged, or the provider is backing off after a failure. Other levels are `warn` and `error`.

The daemon exits with code 2 when the configuration is invalid (the problems are listed together, each naming its instance) and with code 1 on a runtime failure. `SIGINT` and `SIGTERM` stop it gracefully.
