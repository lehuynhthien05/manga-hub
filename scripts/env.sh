# Sourced by the dev scripts. conf NAME DEFAULT prints a setting the same way the
# programs read it: a real environment variable wins, then .env, then the default.
conf() {
  local v="${!1:-}"
  if [ -z "$v" ] && [ -f .env ]; then
    v=$(sed -n "s/^$1=\([^ #]*\).*/\1/p" .env | tail -n 1)
  fi
  echo "${v:-$2}"
}
port() { local a; a=$(conf "$1" "$2"); echo "${a##*:}"; }

HTTP_PORT=$(port HTTP_ADDR :8080)
TCP_PORT=$(port TCP_ADDR :9090)
UDP_PORT=$(port UDP_ADDR :9091)
GRPC_PORT=$(port GRPC_ADDR :9092)
API="http://127.0.0.1:$HTTP_PORT"
