# syntax=docker/dockerfile:1

# The build stage always runs on the builder's own architecture and
# cross-compiles, so multi-platform images do not need emulation.
#
# The toolchain is newer than the go directive of go.mod on purpose: the binary
# carries the standard library of the compiler that built it, so it has to be a
# release that still gets security fixes. Both base images are pinned by digest
# and Dependabot moves the digests.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build

WORKDIR /src

# Modules first: this layer is reused until go.mod or go.sum change, and the
# module cache survives between builds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
# Build tags to compile with, comma-separated (see cmd/dnspatch/main.go and
# "Building from source" in README.md). Empty is the lightweight build, with
# every retriever and provider; the -full image is built with
# TAGS="ping,notify_all"; a small custom image with, for example,
# TAGS="dnspatch_none,ipify,cloudflare".
ARG TAGS
# TARGETVARIANT is "v7" for linux/arm/v7 and empty elsewhere; GOARM wants "7".
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -trimpath -tags "${TAGS}" -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/dnspatch ./cmd/dnspatch

# Static binary, CA certificates for the provider APIs, no shell, non-root.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/dnspatch /usr/local/bin/dnspatch

# The release workflow overwrites title, description, source and licenses with
# the values of the GitHub repository (docker/metadata-action) and adds version,
# revision and created; the same values are here so that a local build is
# labelled too. "source" is also what links the ghcr.io package to the repository.
LABEL org.opencontainers.image.title="dnspatch" \
      org.opencontainers.image.description="Dynamic DNS daemon in Go with a pluggable retriever/provider architecture" \
      org.opencontainers.image.source="https://github.com/KrimsN/dnspatch" \
      org.opencontainers.image.documentation="https://pkg.go.dev/github.com/KrimsN/dnspatch" \
      org.opencontainers.image.licenses="MIT"

USER nonroot:nonroot

# healthcheck reads the same config to learn each instance's interval and
# checks the status file dnspatch itself writes on every completed cycle
# (default $TMPDIR/dnspatch-health, override with DNSPATCH_HEALTH_DIR); no
# shell or curl needed, which a distroless image does not have.
HEALTHCHECK --interval=1m --timeout=10s --start-period=30s --retries=3 \
    CMD ["/usr/local/bin/dnspatch", "healthcheck"]

# The daemon looks for /etc/dnspatch/config.toml on its own; mount the file there.
ENTRYPOINT ["/usr/local/bin/dnspatch"]
