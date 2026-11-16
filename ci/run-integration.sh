#!/usr/bin/env bash
# Copyright 2026 Query Farm LLC - https://query.farm
#
# Run test/sql/*.test through DuckDB (the haybarn-unittest sqllogictest runner)
# against this worker, over one transport. Adapted from the vgi-starter
# (go) ci/run-integration.sh.
#
#   WORKER=/abs/path/to/worker-executable TRANSPORT=stdio ci/run-integration.sh
#
# Required environment:
#   WORKER            absolute path to an executable that serves the worker:
#                     no arguments = stdio; `--http` = HTTP on an ephemeral
#                     loopback port, printing `PORT:<n>` on stdout once listening.
#                     For TRANSPORT=stdio it may be any LOCATION command (the
#                     Docker image_test passes `docker run -i --rm IMG stdio`);
#                     for TRANSPORT=http an http(s):// URL uses that already
#                     running worker instead of starting one.
# Optional:
#   TRANSPORT         stdio | launch | http                        (default: stdio)
#   HAYBARN_UNITTEST  path to haybarn-unittest   (default: the one on PATH)
#   MIN_EXECUTED      fail if fewer test cases ran (default: 1)
#   EXPECTED_SKIP_REASONS  newline-separated skip reasons that are allowed,
#                     e.g. "require-env STARTER_LIVE". Any other skip fails.
#   TEST_PATTERN      which staged files to run (default: test/sql/*)
#   SKIP_MOCK=1       don't start the repo's mock backend (Docker image_test)
#   WARMUP_TIMEOUT    seconds allowed for the extension install (default: 300)
#   KEEP_STAGE=1      keep the staged test tree for debugging
#
# Transports:
#   stdio   DuckDB spawns the worker and talks over stdin/stdout (the default).
#   launch  DuckDB starts the worker once on an AF_UNIX socket and pools
#           connections (`LOCATION 'launch:/path'`); best for slow-starting
#           runtimes such as the JVM.
#   http    the worker runs as an HTTP server (`--http`), as in Docker deploys.
#
# Why the extra checks: the sqllogictest runner turns some errors into silent
# SKIPs (anything mentioning "HTTP" without --test-config) and exits 0 when no
# test matched. Both would make a broken lane look green, so this script passes
# a config that disables error-skipping and fails on any unexpected skip.
set -euo pipefail

: "${WORKER:?absolute path to the worker executable}"
TRANSPORT="${TRANSPORT:-stdio}"
HAYBARN_UNITTEST="${HAYBARN_UNITTEST:-$(command -v haybarn-unittest || true)}"
MIN_EXECUTED="${MIN_EXECUTED:-1}"
TEST_PATTERN="${TEST_PATTERN:-test/sql/*}"
SKIP_MOCK="${SKIP_MOCK:-}"
[ -n "$HAYBARN_UNITTEST" ] || { echo "::error::haybarn-unittest not found (make tools)"; exit 1; }

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
STAGE="$(mktemp -d)"
PIDS=()
WORKER_PID=""
cleanup() {
  for p in ${PIDS[@]+"${PIDS[@]}"}; do kill "$p" 2>/dev/null || true; done
  [ "${KEEP_STAGE:-0}" = "1" ] && echo "stage kept at $STAGE" || rm -rf "$STAGE"
}
trap cleanup EXIT

# start_server <log> <cmd...> — run a server that prints PORT:<n>; sets PORT.
PORT=""
start_server() {
  local log="$1"; shift
  # Create the log before backgrounding: the child opens its redirect only
  # once it is scheduled, and the poll below would otherwise read a missing
  # file, which under set -e -o pipefail kills the whole run.
  : > "$log"
  "$@" >"$log" 2>&1 &
  local pid=$!
  PIDS+=("$pid")
  LAST_PID="$pid"
  for _ in $(seq 1 120); do
    kill -0 "$pid" 2>/dev/null || { echo "::error::'$*' exited early:"; cat "$log"; return 1; }
    PORT="$(sed -n 's/^PORT:\([0-9][0-9]*\).*/\1/p' "$log" | head -1)"
    [ -n "$PORT" ] && return 0
    sleep 0.5
  done
  echo "::error::'$*' never printed PORT:<n>"; cat "$log"; return 1
}

# The mock OData service the table functions are tested against. SKIP_MOCK=1
# (Docker image_test) uses an externally started mock: the containerized worker
# cannot reach one on the host loopback, so the caller runs the mock as a
# container on a shared docker network and pre-sets VGI_ODATA_TEST_URL.
if [ -n "$SKIP_MOCK" ]; then
  EXPECTED_SKIP_REASONS="${EXPECTED_SKIP_REASONS:-}"$'\n'"require-env VGI_ODATA_TEST_URL"
else
  ( cd "$REPO" && go build -o "$STAGE/mockserver" ./cmd/mockserver )
  start_server "$STAGE/mock.log" "$STAGE/mockserver" --addr 127.0.0.1:0
  export VGI_ODATA_TEST_URL="http://127.0.0.1:$PORT"
fi

HTTP=0
case "$TRANSPORT" in
  stdio)  export VGI_ODATA_WORKER="$WORKER" ;;
  launch) export VGI_ODATA_WORKER="launch:$WORKER" ;;
  http)
    HTTP=1
    case "$WORKER" in
      http://*|https://*) export VGI_ODATA_WORKER="$WORKER" ;;   # an already-running worker
      *)
        start_server "$STAGE/worker.log" "$WORKER" --http
        WORKER_PID="$LAST_PID"
        export VGI_ODATA_WORKER="http://127.0.0.1:$PORT"
        ;;
    esac
    ;;
  *) echo "::error::unknown TRANSPORT=$TRANSPORT (expected stdio|launch|http)"; exit 1 ;;
esac

# Stage the tests, rewriting `require vgi` into a LOAD of the signed extension
# (the standalone runner links no extensions; most of these files already say
# `LOAD vgi;`), `require <core ext>` into INSTALL + LOAD, and loading httpfs
# after vgi for the HTTP transport.
mkdir -p "$STAGE/test/sql"
for f in "$REPO"/test/sql/*.test; do
  awk -v http="$HTTP" '
    /^require[ \t]+vgi[ \t]*$/ || /^LOAD[ \t]+vgi;[ \t]*$/ {
      if ($1 == "require") { print "statement ok"; print "LOAD vgi;" } else print
      if (http == 1) { print ""; print "statement ok"; print "LOAD httpfs;" }
      next
    }
    /^require[ \t]+(httpfs|json|parquet|spatial)[ \t]*$/ {
      print "statement ok"; print "INSTALL " $2 " FROM core;"; print ""
      print "statement ok"; print "LOAD " $2 ";"; next
    }
    { print }' "$f" > "$STAGE/test/sql/$(basename "$f")"
done

# Install the extensions once, refreshing any stale cached copy of vgi.
cat > "$STAGE/test/_warm.test" <<'EOF'
statement ok
FORCE INSTALL vgi FROM community;

statement ok
INSTALL httpfs FROM core;

statement ok
INSTALL json FROM core;

statement ok
INSTALL parquet FROM core;
EOF
# Bounded: a stalled extension download would otherwise hang the lane until the
# job timeout. WARMUP_TIMEOUT is in seconds.
"$HAYBARN_UNITTEST" --keep-home --test-dir "$STAGE" "test/_warm.test" >/dev/null 2>&1 &
warm_pid=$!
warm_deadline=$((SECONDS + ${WARMUP_TIMEOUT:-300}))
while kill -0 "$warm_pid" 2>/dev/null && [ "$SECONDS" -lt "$warm_deadline" ]; do sleep 1; done
if kill -0 "$warm_pid" 2>/dev/null; then
  kill "$warm_pid" 2>/dev/null || true
  wait "$warm_pid" 2>/dev/null || true
  echo "::warning::extension warm-up timed out after ${WARMUP_TIMEOUT:-300}s (stalled download?)"
elif ! wait "$warm_pid"; then
  echo "::warning::extension warm-up did not fully succeed"
fi
rm -f "$STAGE/test/_warm.test"

cat > "$STAGE/no_error_skip.json" <<'EOF'
{"skip_error_messages": ["__vgi_starter_never_matches__"]}
EOF

echo "Running $TEST_PATTERN over $TRANSPORT (worker: $VGI_ODATA_WORKER)"
LOG="$STAGE/unittest.log"
rc=0
( cd "$STAGE" && "$HAYBARN_UNITTEST" --keep-home --test-dir "$STAGE" \
    --test-config "$STAGE/no_error_skip.json" "$TEST_PATTERN" ) 2>&1 | tee "$LOG" && rc=0 || rc="${PIPESTATUS[0]}"

if grep -q 'No test cases matched\|No tests ran' "$LOG"; then
  echo "::error::no test cases matched; the staging is wrong"; exit 1
fi
total="$(sed -n 's/^\[[0-9]*\/\([0-9]*\)\].*/\1/p' "$LOG" | tail -1)"
total="${total:-0}"
skips="$(awk '/^Skipped tests for the following reasons:/{b=1;next} b&&/^[[:space:]]*$/{b=0} b&&match($0,/: [0-9]+[[:space:]]*$/){print substr($0,1,RSTART-1) "\t" substr($0,RSTART+2)+0}' "$LOG")"
skipped=0
while IFS=$'\t' read -r reason n; do
  [ -n "$reason" ] || continue
  skipped=$((skipped + n))
  if ! printf '%s\n' "${EXPECTED_SKIP_REASONS:-}" | grep -qxF "$reason"; then
    echo "::error::unexpected skip: '$reason'. A test file silently stopped running;" \
         "fix the gate or add it to EXPECTED_SKIP_REASONS."
    rc=1
  fi
done <<< "$skips"
executed=$((total - skipped))
echo "executed $executed of $total test files over $TRANSPORT ($skipped skipped)"
if [ "$executed" -lt "$MIN_EXECUTED" ]; then
  echo "::error::only $executed test files ran (MIN_EXECUTED=$MIN_EXECUTED)"; rc=1
fi
if [ -n "$WORKER_PID" ] && ! kill -0 "$WORKER_PID" 2>/dev/null; then
  echo "::error::the HTTP worker died during the run:"; cat "$STAGE/worker.log"; rc=1
fi
exit "$rc"
