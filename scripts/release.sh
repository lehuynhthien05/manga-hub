#!/usr/bin/env bash
# Usage: scripts/release.sh <manga-id> [chapter]
# Logs in as the admin and releases the next (or the given) chapter; the backend
# then sends a best-effort UDP alert to every subscribed TUI.
# Needs ADMIN_PASSWORD in the environment: set -a; source .env; set +a
set -euo pipefail
API="${MANGAHUB_API:-http://127.0.0.1:8080}"
ID="${1:?usage: release.sh <manga-id> [chapter]}"
TOKEN=$(curl -fsS -X POST "$API/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN_USERNAME:-admin}\",\"password\":\"${ADMIN_PASSWORD:?set ADMIN_PASSWORD}\"}" |
  python3 -c 'import sys, json; print(json.load(sys.stdin)["token"])')
BODY='{}'
if [ $# -ge 2 ]; then BODY="{\"chapter\":$2}"; fi
curl -sS -X POST "$API/api/admin/manga/$ID/release" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "$BODY"
echo
