#!/usr/bin/env bash
# DESTRUCTIVE: stops the stack and deletes both SQLite databases (users, library,
# catalog changes). The next dev-up.sh re-seeds the catalog (seeded=35).
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/env.sh
scripts/dev-down.sh
for db in "$(conf BACKEND_DB_PATH var/backend.db)" "$(conf CATALOG_DB_PATH var/catalog.db)"; do
  rm -f "$db" "$db-wal" "$db-shm"
  echo "deleted $db"
done
