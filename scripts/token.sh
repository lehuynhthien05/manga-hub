#!/bin/sh
# Usage: scripts/token.sh <username> <password> [api-base]
# Logs in and prints only the JWT (for curl/nc checks). Never commit its output.
set -eu
api="${3:-${MANGAHUB_API:-http://127.0.0.1:8080}}"
curl -fsS -X POST "$api/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$1\",\"password\":\"$2\"}" |
  python3 -c 'import sys, json; print(json.load(sys.stdin)["token"])'
