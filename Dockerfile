# syntax=docker/dockerfile:1

# The build stage always runs on the builder's own architecture and
# cross-compiles, so multi-platform images do not need emulation.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build

WORKDIR /src

# Modules first: this layer is reused until go.mod or go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
# TARGETVARIANT is "v7" for linux/arm/v7 and empty elsewhere; GOARM wants "7".
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/dnspatch ./cmd/dnspatch

# Static binary, CA certificates for the provider APIs, no shell, non-root.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/dnspatch /usr/local/bin/dnspatch

USER nonroot:nonroot

# The daemon looks for /etc/dnspatch/config.toml on its own; mount the file there.
ENTRYPOINT ["/usr/local/bin/dnspatch"]
