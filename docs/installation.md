# Installation

## Binary

Download the archive for your platform from the [releases page](https://github.com/dnspatch/dnspatch/releases): Linux (amd64, arm64, armv7), macOS and Windows (amd64, arm64). Each release carries a `checksums.txt`.

Releases ship two builds of the daemon:

| Build | Archive | What it is |
|-------|---------|------------|
| lightweight | `dnspatch_*` | every retriever and provider, none of the optional monitoring features |
| full | `dnspatch-full_*` | the same, plus the `ping_url` hook and the notifiers that publish to a message broker (see [Monitoring](operations/monitoring.md)) |

Nothing else changes between them, and a config that uses none of the optional features behaves identically on both.

## Docker

The image is published to [Docker Hub](https://hub.docker.com/r/krimsn/dnspatch) (`krimsn/dnspatch`) and mirrored to the GitHub Container Registry (`ghcr.io/dnspatch/dnspatch`) under the same tags: `0.1.0`, `0.1` and `latest` for the lightweight build, `0.1.0-full` and `latest-full` for the full one. `latest` follows the newest stable release; pin a version tag in production, since a `v0.x` minor release may change the configuration format.

Running it is covered on its own page: [Docker](deployment/docker.md).

## Building from source

```sh
go install github.com/dnspatch/dnspatch/cmd/dnspatch@latest
```

Requires Go 1.25 or newer. A plain `go build` gives the same daemon as the lightweight image and binaries. To get a smaller binary, or the features of the full one, choose what goes in with build tags: see [Building from source](deployment/building.md).
