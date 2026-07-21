# Copyright 2026 Query Farm LLC - https://query.farm
#
# Single image that serves the network transports of the `vgi-odata` VGI worker:
#   docker run ... IMG            -> HTTP server on $PORT      (default; Fly.io / local)
#   docker run -i ... IMG stdio   -> stdio worker DuckDB spawns on-host
#   docker run ... IMG mock       -> the standalone mock OData server (CI E2E only)
# See docker-entrypoint.sh.
#
# The worker is STATELESS: it reads OData v2/v4 services live over HTTP and holds
# no local data, so there is no /data volume and no `farm.query.vgi.volumes`
# mount-discovery label — the image is just the binaries + a tiny entrypoint.
# syntax=docker/dockerfile:1

# ---- build stage -----------------------------------------------------------
# CGO is REQUIRED: the vgi-go / vgi-rpc-go SDK links DuckDB statically (via
# duckdb-go-bindings) for the Arrow C Data Interface, so CGO_ENABLED=0 fails to
# link. gcc/g++ + libc headers back the cgo compile. The reusable workflow builds
# one image per arch on a native runner, so there is no cross-compilation.
FROM golang:1.26-bookworm AS build
WORKDIR /src

ENV CGO_ENABLED=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        gcc g++ libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Resolve modules first (cached independently of the source) so ordinary code
# edits do not re-download the module graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal

# BuildKit cache mounts persist the module cache and the compiled Go/CGO objects
# across image rebuilds, so incremental changes only recompile what changed and
# the DuckDB-linked tree is not rebuilt from scratch every time. Build BOTH the
# worker and the mock OData server: the mock backs the image_test's offline SQL
# suite (the worker's table functions always make a live HTTP call).
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -ldflags="-s -w" -o /worker ./cmd/vgi-odata-worker \
 && go build -trimpath -ldflags="-s -w" -o /mockserver ./cmd/mockserver

# ---- runtime stage ---------------------------------------------------------
# debian-slim (not distroless) so the HEALTHCHECK below has a real `curl`.
FROM debian:bookworm-slim

# Build metadata, wired from docker/metadata-action outputs in CI.
ARG VERSION=0.0.0
ARG GIT_COMMIT=unknown
ARG SOURCE_URL=https://github.com/Query-farm/vgi-odata

# Standard OCI labels + the VGI transport-advertisement label. `transports`
# lists the NETWORK transports this image serves (http); stdio is a spawn mode,
# not a network transport, so it is not listed. `mock` is a CI-only helper.
LABEL org.opencontainers.image.title="vgi-odata" \
      org.opencontainers.image.description="Query OData v2/v4 services (Dynamics, SAP Gateway, and other enterprise REST APIs) and return their entities as DuckDB rows — a VGI worker (stdio + HTTP)" \
      org.opencontainers.image.source="${SOURCE_URL}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.licenses="MIT" \
      farm.query.vgi.transports='["http"]'

ENV PORT=8000 \
    VGI_ODATA_GIT_COMMIT=${GIT_COMMIT}

WORKDIR /app

# ca-certificates: the worker fetches OData services over HTTPS (e.g. the public
# TripPin reference service). curl backs the HEALTHCHECK below.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

# `--chmod` sets the mode in the COPY layer itself. A separate `RUN chmod` would
# rewrite the whole (large, DuckDB-linked) binary into a second layer.
COPY --from=build --chmod=0755 /worker /usr/local/bin/vgi-odata-worker
COPY --from=build --chmod=0755 /mockserver /usr/local/bin/mockserver
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Run unprivileged. No state, no volume — there is nothing to own or persist.
RUN useradd --create-home --uid 10001 app
USER app

EXPOSE 8000

# Readiness probe for HTTP mode. Inert for a short-lived stdio container, which
# has no HTTP server (the probe just fails harmlessly there).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS "http://localhost:${PORT:-8000}/health" || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["http"]
