#!/usr/bin/env bash
# Deploy the :latest MangaHub images to the server.
#
#   DEPLOY_HOST=... DEPLOY_USER=... DEPLOY_PATH=/opt/mangahub \
#   GHCR_USER=<github user> GHCR_TOKEN=<token with read:packages> ./deploy.sh
#
# Reads the production env file from ENV_FILE (default .env.production).
# Run from the repository root. Contains no secret and asks for no password.
set -euo pipefail

ENV_FILE="${ENV_FILE:-.env.production}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"   # seconds
REQUIRED_ENV_KEYS=(JWT_SECRET ADMIN_PASSWORD)
SERVICES="catalog backend"

fail() { echo "deploy: $*" >&2; exit 1; }

# 1. Preconditions -----------------------------------------------------------
missing=()
for v in DEPLOY_HOST DEPLOY_USER DEPLOY_PATH GHCR_USER GHCR_TOKEN; do
  [ -n "${!v:-}" ] || missing+=("$v")
done
[ "${#missing[@]}" -eq 0 ] || fail "missing environment variables: ${missing[*]}"
[[ "$DEPLOY_PATH" =~ ^/[A-Za-z0-9._/-]+$ ]] || fail "DEPLOY_PATH must be an absolute path without spaces"
[[ "$GHCR_USER" =~ ^[A-Za-z0-9-]+$ ]] || fail "GHCR_USER is not a valid GitHub user name"
[[ "$HEALTH_TIMEOUT" =~ ^[0-9]+$ ]] || fail "HEALTH_TIMEOUT must be a number of seconds"
[ -f compose.yaml ] || fail "compose.yaml not found: run this from the repository root"
[ -f "$ENV_FILE" ] || fail "env file not found: $ENV_FILE"

bad=()
for k in "${REQUIRED_ENV_KEYS[@]}"; do
  line="$(grep -E "^${k}=" "$ENV_FILE" | tail -n 1 || true)"
  val="${line#*=}"; val="${val%\"}"; val="${val#\"}"
  if [ -z "$val" ] || [[ "$val" == change-me* ]]; then bad+=("$k"); fi
done
[ "${#bad[@]}" -eq 0 ] || fail "empty or placeholder values in $ENV_FILE: ${bad[*]}"

remote="$DEPLOY_USER@$DEPLOY_HOST"
ssh_opts=(-o BatchMode=yes -o ConnectTimeout=10)
ssh "${ssh_opts[@]}" "$remote" true || fail "cannot connect to $remote over SSH"

# 2. Upload compose.yaml and the env file (as .env, mode 600) -----------------
ssh "${ssh_opts[@]}" "$remote" "mkdir -p '$DEPLOY_PATH'"
scp -q "${ssh_opts[@]}" compose.yaml "$remote:$DEPLOY_PATH/compose.yaml"
ssh "${ssh_opts[@]}" "$remote" "umask 077 && cat > '$DEPLOY_PATH/.env' && chmod 600 '$DEPLOY_PATH/.env'" < "$ENV_FILE"

# 8. Always log out of the registry, also on failure --------------------------
cleanup() { ssh "${ssh_opts[@]}" "$remote" "docker logout ghcr.io" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# 3. Registry login: the token travels over SSH stdin, never as an argument ---
printf '%s' "$GHCR_TOKEN" | ssh "${ssh_opts[@]}" "$remote" "docker login ghcr.io -u '$GHCR_USER' --password-stdin" >/dev/null \
  || fail "docker login to ghcr.io failed on the server"

# 4-10. Pull, start, wait for health -------------------------------------------
ssh "${ssh_opts[@]}" "$remote" \
  "DEPLOY_PATH='$DEPLOY_PATH' HEALTH_TIMEOUT='$HEALTH_TIMEOUT' SERVICES='$SERVICES' bash -s" <<'REMOTE'
set -euo pipefail
cd "$DEPLOY_PATH"

# Without this the server keeps running its old local copy of :latest.
docker compose pull

# No separate migration step: catalog and backend apply their embedded
# schema.sql (CREATE TABLE IF NOT EXISTS) when they start.
docker compose up -d --no-build --remove-orphans

deadline=$((SECONDS + HEALTH_TIMEOUT))
for svc in $SERVICES; do
  while :; do
    cid="$(docker compose ps -q "$svc" | head -n 1)"
    status=missing
    if [ -n "$cid" ]; then
      status="$(docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null || echo unknown)"
    fi
    [ "$status" = healthy ] && break
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "deploy: $svc is '$status' after ${HEALTH_TIMEOUT}s. Last log lines:" >&2
      docker compose logs --tail=50 "$svc" >&2 || true
      exit 1
    fi
    sleep 3
  done
  echo "deploy: $svc is healthy"
done

# End-to-end check: the backend must reach the catalog over gRPC.
health="$(docker compose exec -T backend curl -fsS http://127.0.0.1:8080/api/health)"
echo "deploy: /api/health -> $health"
case "$health" in
  *'"catalog":"up"'*) ;;
  *) echo "deploy: backend cannot reach the catalog. Last backend log lines:" >&2
     docker compose logs --tail=50 backend >&2 || true
     exit 1 ;;
esac

# 9. Remove the images the overwritten :latest tag left behind.
docker image prune -f >/dev/null
docker compose ps
REMOTE

commit="${GITHUB_SHA:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}"
echo "deploy: done, commit ${commit:0:12} is running on $DEPLOY_HOST"
