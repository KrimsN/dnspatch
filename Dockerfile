# syntax=docker/dockerfile:1

# The build stage always runs on the builder's own architecture and
# cross-compiles, so multi-platform images do not need emulation.
#
# The toolchain is newer than the go directive of go.mod on purpose: the binary
# carries the standard library of the compiler that built it, so it has to be a
# release that still gets security fixes. Both base images are pinned by digest
# and Dependabot moves the digests.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build

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
# TARGETVARIANT is "v7" for linux/arm/v7 and empty elsewhere; GOARM wants "7".
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/dnspatch ./cmd/dnspatch

# Static binary, CA certificates for the provider APIs, no shell, non-root.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/dnspatch /usr/local/bin/dnspatch

USER nonroot:nonroot

# The daemon looks for /etc/dnspatch/config.toml on its own; mount the file there.
ENTRYPOINT ["/usr/local/bin/dnspatch"]
