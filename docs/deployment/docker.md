# Docker

The image is built for `linux/amd64`, `linux/arm64` and `linux/arm/v7`. It runs as an unprivileged user (uid 65532) and looks for its configuration at `/etc/dnspatch/config.toml`. The configuration is not baked into the image: mount it, and changing a setting means editing the file and restarting the container, never rebuilding.

## Docker Compose

With [compose.yml](https://github.com/dnspatch/dnspatch/blob/main/compose.yml):

```sh
cp dnspatch.toml.example dnspatch.toml   # edit it
cp .env.example .env                   # put the secrets in it
chmod 644 dnspatch.toml                # the container user must be able to read it
docker compose up -d
```

Every variable in `.env` reaches the container, and `dnspatch.toml` refers to it as `${NAME}`. After editing `dnspatch.toml`, run `docker compose restart`. After editing `.env`, run `docker compose up -d`, which recreates the container with the new environment; `restart` does not re-read it.

## Without Compose

```sh
docker run -d --name dnspatch --restart unless-stopped \
  -v "$PWD/dnspatch.toml:/etc/dnspatch/config.toml:ro" \
  --env-file .env \
  --log-opt max-size=10m --log-opt max-file=3 \
  krimsn/dnspatch:latest
```

## Image tags

The image is published to [Docker Hub](https://hub.docker.com/r/krimsn/dnspatch) (`krimsn/dnspatch`) and mirrored to the GitHub Container Registry (`ghcr.io/dnspatch/dnspatch`) under the same tags.

| Tag | Build |
|-----|-------|
| `0.1.0`, `0.1`, `latest` | lightweight: every retriever and provider, no optional monitoring |
| `0.1.0-full`, `latest-full` | full: also the `ping_url` hook and the notifiers |

`latest` follows the newest stable release; pin a version tag in production, since a `v0.x` minor release may change the configuration format. To build your own image with chosen plugins, see [Building from source](building.md#docker-build-argument).

## Things to know

### Keep secrets out of `dnspatch.toml`

The `chmod 644` above makes the file readable by every user on the host, so a password written into it is readable too. Write `password = "${PASSWORD}"` and put the value in `.env`, which stays private (`chmod 600 .env`).

### `.env` is not a vault

The values become environment variables of the container, and `docker inspect` prints them. Whoever can talk to the Docker daemon can read your secrets. Docker/Swarm and Kubernetes secrets avoid this: they are mounted as files, not environment variables. Write `password = "${file:/run/secrets/password}"` instead, and add the secret to `compose.yml`:

```yaml
services:
  dnspatch:
    secrets:
      - password
secrets:
  password:
    file: ./secrets/password.txt
```

### Limit the logs

Docker keeps container logs without a size limit unless told otherwise. `compose.yml` rotates them at three files of 10 MB; the `--log-opt` flags above do the same for `docker run`.

### No IPv6 by default

The default bridge network of Docker has no IPv6, so a retriever with `family = "ipv6"` cannot reach ifconfig.co and fails on every tick. Give the container a network with IPv6 enabled, or on Linux run it with `network_mode: host`. `family = "ipv4"` (the default) needs nothing.

### `HEALTHCHECK` needs a writable `/tmp`

The image runs `dnspatch healthcheck` on its own (see [Monitoring](../operations/monitoring.md#health-check)); with `read_only: true`, as `compose.yml` sets, mount `/tmp` as `tmpfs` too, or the check always reports the daemon as stuck.
