#!/usr/bin/env bash
# Process churn: kills the catalog and the backend in turn (stack from dev-up.sh),
# probes during the outage, restarts it and times the recovery. Usage: chaos.sh [rounds]
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/env.sh
ROUNDS="${1:-6}"
mkdir -p docs/evidence
LOG="docs/evidence/13-chaos-$(date +%Y%m%d-%H%M).txt"
now() { python3 -c 'import time; print(time.time())'; }
probe() { curl -s -o /dev/null -w '%{http_code} %{time_total}s' -m 3 "$API/api/manga/one-piece" || true; }
for i in $(seq 1 "$ROUNDS"); do
  target=$([ $((i % 2)) -eq 0 ] && echo backend || echo catalog)
  pid=$(cat "var/$target.pid")
  echo "round $i: SIGTERM $target ($pid)" | tee -a "$LOG"
  kill "$pid"; while kill -0 "$pid" 2>/dev/null; do sleep 0.05; done
  echo "  during the outage: $(probe)" | tee -a "$LOG"
  ./bin/$target >> "var/$target.out" 2>&1 & echo $! > "var/$target.pid"
  t0=$(now)
  until [ "$(probe | cut -d' ' -f1)" = "200" ]; do sleep 0.05; done
  echo "  first 200 after $(python3 -c "import time; print('%.2f' % (time.time() - $t0))") s" | tee -a "$LOG"
done
echo "log: $LOG"
