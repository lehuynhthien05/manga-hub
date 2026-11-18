#!/usr/bin/env bash
# Builds and starts catalog then backend in the background (logs in var/*.out,
# pids in var/*.pid). Stop them with scripts/dev-down.sh.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/env.sh
mkdir -p var bin
make -s build
for p in "$HTTP_PORT" "$TCP_PORT" "$GRPC_PORT"; do
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then echo "port $p (tcp) is in use — run scripts/dev-down.sh or free it" >&2; exit 1; fi
done
if lsof -nP -iUDP:"$UDP_PORT" >/dev/null 2>&1; then echo "port $UDP_PORT (udp) is in use — run scripts/dev-down.sh or free it" >&2; exit 1; fi

./bin/catalog > var/catalog.out 2>&1 & echo $! > var/catalog.pid
for _ in $(seq 1 100); do nc -z 127.0.0.1 "$GRPC_PORT" 2>/dev/null && break; sleep 0.1; done
./bin/backend > var/backend.out 2>&1 & echo $! > var/backend.pid
for _ in $(seq 1 100); do curl -fsS "$API/api/health" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "$API/api/health" || { echo "backend did not come up — see var/backend.out" >&2; exit 1; }
echo
echo "up — start a TUI with ./bin/tui (logs: var/catalog.out, var/backend.out)"
