#!/bin/sh
# Copyright 2026 Query Farm LLC - https://query.farm
#
# Dispatch the single vgi-odata image into one of its modes:
#   http   (default) the HTTP server on $PORT (8000), bound 0.0.0.0 so a
#                    published host port reaches it. Serves /health.
#   stdio            a worker DuckDB spawns over stdio (on-host execution).
#   mock             the standalone mock OData server (CI E2E only) on $PORT,
#                    bound 0.0.0.0; $MOCK_BASE_URL sets the absolute nextLink so
#                    a containerized worker can page it (see ci/run-integration.sh).
# Any other first argument is exec'd verbatim (escape hatch for debugging).
#
# The worker is stateless (reads OData live over HTTP), so there is no /data to
# create and no state env to wire — each mode just exec's the binary.
set -e

case "${1:-http}" in
  http)
    shift 2>/dev/null || true
    # Bind 0.0.0.0 on a FIXED port so `-p $PORT:$PORT` and the HEALTHCHECK reach
    # it. In dev/CI the default (--http, 127.0.0.1:0) is unchanged; only the
    # container overrides the address here.
    exec vgi-odata-worker --http --http-addr "0.0.0.0:${PORT:-8000}" "$@"
    ;;
  stdio)
    shift 2>/dev/null || true
    exec vgi-odata-worker "$@"
    ;;
  mock)
    shift 2>/dev/null || true
    # CI-only: the mock OData service the SQL suite queries. --base-url makes the
    # advertised @odata.nextLink resolve to an address the worker container can
    # reach (a docker-network hostname), so paging works across containers.
    exec mockserver --addr "0.0.0.0:${PORT:-8000}" ${MOCK_BASE_URL:+--base-url "$MOCK_BASE_URL"} "$@"
    ;;
  *)
    exec "$@"
    ;;
esac
