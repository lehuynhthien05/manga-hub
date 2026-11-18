#!/usr/bin/env bash
# Stops what dev-up.sh started: SIGTERM, then waits up to 5 s per process.
set -uo pipefail
cd "$(dirname "$0")/.."
for name in backend catalog; do # backend first: it depends on the catalog
  f="var/$name.pid"
  [ -f "$f" ] || continue
  pid=$(cat "$f")
  if kill -TERM "$pid" 2>/dev/null; then
    for _ in $(seq 1 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
    if kill -0 "$pid" 2>/dev/null; then echo "$name ($pid) did not stop in 5 s" >&2; else echo "$name stopped"; fi
  fi
  rm -f "$f"
done
