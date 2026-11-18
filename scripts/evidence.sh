#!/usr/bin/env bash
# Captures raw, unedited test evidence into docs/evidence/11-<date-time>/.
set -uo pipefail
cd "$(dirname "$0")/.."
OUT=docs/evidence/11-$(date +%Y%m%d-%H%M)
mkdir -p "$OUT"
{ go version; uname -a; echo "commit $(git rev-parse --short HEAD)$(git diff --quiet HEAD -- . ":!docs" || echo " + uncommitted changes")"; } > "$OUT/env.txt"
go test -race -count=1 -v ./... > "$OUT/go-test-race.txt" 2>&1; echo "go test -race exit=$?" | tee -a "$OUT/env.txt"
go test -cover ./... > "$OUT/coverage.txt" 2>&1
echo "evidence in $OUT"
