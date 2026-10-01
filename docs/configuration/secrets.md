# Secrets

Passwords, tokens and keys do not belong in the configuration file in plain text. dnspatch reads them from the environment or from files.

## Environment variables

```toml
[provider.regru]
type     = "regru"
username = "my-login"
password = "${PASSWORD}"
```

`${NAME}` inside a string is replaced with the environment variable. A variable that is not set is an error, not an empty string. Write `$${` for a literal `${`.

## Files

```toml
password = "${file:/run/secrets/password}"
```

`${file:/path}` is replaced with the contents of the file, minus one trailing newline. An unreadable file is an error. This is how Docker and Kubernetes secrets, mounted as files, reach the config, and it keeps the value out of the process environment. A relative path is resolved against the working directory; [secrets-from-files.toml](https://github.com/dnspatch/dnspatch/blob/main/examples/secrets-from-files.toml) shows the form.

## In containers

Environment variables are not a vault: they become variables of the container, and `docker inspect` prints them. See [Docker](../deployment/docker.md#keep-secrets-out-of-dnspatchtoml) for the trade-offs and how to use Docker or Swarm secrets.

## In logs and `--check-config`

Parameters marked `secret` in the [parameter reference](../PARAMETERS.md) are never printed by `--check-config`: they show up as `***` only when passwords alone tell the providers apart, and are left out whenever anything else differs. Proxy URLs are never printed in logs or errors, since they may hold a password.
