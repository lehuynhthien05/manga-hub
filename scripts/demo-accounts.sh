#!/usr/bin/env bash
# Registers the demo users (an existing one is fine) and checks the admin login.
# These are demo values only, also listed in the README.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/env.sh
printf '%-8s %-11s %s\n' user password result
for pair in alice:Alice1234 bob:Bob12345 charlie:Charlie123; do
  u=${pair%%:*} pw=${pair#*:}
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/auth/register" \
    -H 'Content-Type: application/json' -d "{\"username\":\"$u\",\"password\":\"$pw\"}")
  case $code in 201) r=created ;; 409) r="already exists" ;; *) r="failed ($code)" ;; esac
  printf '%-8s %-11s %s\n' "$u" "$pw" "$r"
done
admin=$(conf ADMIN_USERNAME admin)
pw=$(conf ADMIN_PASSWORD "")
if [ -n "$pw" ] && scripts/token.sh "$admin" "$pw" "$API" >/dev/null 2>&1; then
  echo "admin login ($admin): ok"
else
  echo "admin login ($admin): FAILED — is ADMIN_PASSWORD set in .env?" >&2; exit 1
fi
