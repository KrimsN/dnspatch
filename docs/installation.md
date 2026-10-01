# Installation

=== "Binary"

    Download the archive for your platform from the [releases page](https://github.com/dnspatch/dnspatch/releases): Linux (amd64, arm64, armv7), macOS and Windows (amd64, arm64). Each release carries a `checksums.txt`.

=== "Docker"

    ```sh
    docker run -d --name dnspatch --restart unless-stopped \
      -v "$PWD/dnspatch.toml:/etc/dnspatch/config.toml:ro" \
      --env-file .env \
      krimsn/dnspatch:latest
    ```

    The details, including Docker Compose, secrets and IPv6, are on the [Docker](deployment/docker.md) page.

=== "Go"

    ```sh
    go install github.com/dnspatch/dnspatch/cmd/dnspatch@latest
    ```

    Requires Go 1.25 or newer. A plain `go build` gives the same daemon as the lightweight image and binaries. To get a smaller binary, or the features of the full one, choose what goes in with build tags: see [Building from source](deployment/building.md).

## Lightweight and full builds

Releases ship two builds of the daemon:

| Build | Archive | Image tags | What it is |
|-------|---------|------------|------------|
| lightweight | `dnspatch_*` | `0.4.0`, `0.4`, `latest` | every retriever and provider, none of the optional monitoring features |
| full | `dnspatch-full_*` | `0.4.0-full`, `0.4-full`, `latest-full` | the same, plus the `ping_url` hook and the notifiers that publish to a message broker (see [Monitoring](operations/monitoring.md)) |

The full build exists from 0.4.0 on. Releases 0.1 to 0.3 had a single build, the lightweight one.

Nothing else changes between them, and a config that uses none of the optional features behaves identically on both.

The image is published to [Docker Hub](https://hub.docker.com/r/krimsn/dnspatch) (`krimsn/dnspatch`) and mirrored to the GitHub Container Registry (`ghcr.io/dnspatch/dnspatch`) under the same tags. `latest` follows the newest stable release; pin a version tag in production, since a `v0.x` minor release may change the configuration format.
