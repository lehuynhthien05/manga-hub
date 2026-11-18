# syntax=docker/dockerfile:1
# One Dockerfile for both server programs. Each one is a build target:
#   docker build --target catalog .      docker build --target backend .
# The TUI is a client that runs on the user's machine; it is not containerized.

# ---- build stage: Go toolchain + gcc (the SQLite driver mattn/go-sqlite3 needs CGO)
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/catalog ./cmd/catalog \
 && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/backend ./cmd/backend

# ---- shared runtime base: same Debian release as the build stage (glibc must match)
FROM debian:bookworm-slim AS base
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --user-group --no-create-home app \
 && mkdir /data && chown app:app /data
USER app
# SQLite files live in /data (a named volume in compose.yaml)
WORKDIR /data

# ---- catalog: gRPC Catalog Service
FROM base AS catalog
COPY --from=build /out/catalog /usr/local/bin/catalog
EXPOSE 9092
ENTRYPOINT ["catalog"]

# ---- backend: REST + WebSocket (+ TCP/UDP in later phases)
FROM base AS backend
COPY --from=build /out/backend /usr/local/bin/backend
EXPOSE 8080 9090 9091/udp
ENTRYPOINT ["backend"]
