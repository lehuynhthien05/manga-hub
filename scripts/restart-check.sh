#!/usr/bin/env bash
# Process-level persistence check: write through the API, stop and restart both
# programs (same databases), read it back. Prints PASS or FAIL (exit 1).
# It adds the user "restartcheck" and a library row to whatever databases the
# configuration points at; point BACKEND_DB_PATH / CATALOG_DB_PATH elsewhere to avoid that.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/env.sh
J='Content-Type: application/json'
fail() { echo "FAIL: $*"; scripts/dev-down.sh >/dev/null; exit 1; }

scripts/dev-down.sh >/dev/null
scripts/dev-up.sh >/dev/null
curl -s -o /dev/null -X POST "$API/api/auth/register" -H "$J" -d '{"username":"restartcheck","password":"Restart123"}'
TOKEN=$(scripts/token.sh restartcheck Restart123 "$API")
curl -s -o /dev/null -X POST "$API/api/library" -H "Authorization: Bearer $TOKEN" -H "$J" -d '{"manga_id":"naruto","status":"reading"}'
before=$(curl -fsS -X PUT "$API/api/library/naruto/progress" -H "Authorization: Bearer $TOKEN" -H "$J" -d '{"current_chapter":42}')
total_before=$(curl -fsS "$API/api/manga/naruto" | python3 -c 'import sys,json;print(json.load(sys.stdin)["total_chapters"])')
v_before=$(echo "$before" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["current_chapter"], d["version"])')

scripts/dev-down.sh >/dev/null
scripts/dev-up.sh >/dev/null
TOKEN=$(scripts/token.sh restartcheck Restart123 "$API") || fail "login after restart"
v_after=$(curl -fsS "$API/api/library" -H "Authorization: Bearer $TOKEN" |
  python3 -c 'import sys,json;d=[i for i in json.load(sys.stdin)["items"] if i["manga_id"]=="naruto"][0];print(d["current_chapter"], d["version"])')
total_after=$(curl -fsS "$API/api/manga/naruto" | python3 -c 'import sys,json;print(json.load(sys.stdin)["total_chapters"])')
scripts/dev-down.sh >/dev/null

echo "library naruto (chapter version): before=$v_before after=$v_after"
echo "catalog naruto total_chapters:    before=$total_before after=$total_after"
[ "$v_before" = "$v_after" ] || fail "library row changed across the restart"
[ "$total_before" = "$total_after" ] || fail "catalog changed across the restart"
echo PASS
