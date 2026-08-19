#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_API_IMAGE:-szcq/openvpn-server:api-smoke}"
REQUIRED="${OVPN_API_REQUIRED:-0}"
SKIP_BUILD="${OVPN_API_SKIP_BUILD:-0}"
WORK_DIR=''
CONTAINER=''

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'REST API smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'REST API smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  if [ -n "$CONTAINER" ]; then
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rmdir "$WORK_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
command -v curl >/dev/null 2>&1 || skip_or_fail 'missing command: curl'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'

if [ "$SKIP_BUILD" != 1 ]; then
  OVPN_BUILD_NETWORK=host "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-api.XXXXXX")"
mkdir -m 0750 "$WORK_DIR/data" "$WORK_DIR/config"
cat >"$WORK_DIR/config/config.yaml" <<'YAML'
version: 1
server:
  endpoint: vpn.example.test
ipv4:
  network: 10.71.0.0/24
YAML

run_ovpn() {
  docker run --rm \
    -v "$WORK_DIR/data:/etc/openvpn" \
    -v "$WORK_DIR/config:/etc/ovpn-conf" \
    --entrypoint ovpn \
    "$IMAGE" "$@"
}

run_ovpn server init >"$WORK_DIR/init.out"
run_ovpn api key create frontend-smoke --json >"$WORK_DIR/key.json"
api_key="$(sed -n 's/.*"secret":"\([^"]*\)".*/\1/p' "$WORK_DIR/key.json")"
[[ "$api_key" =~ ^ovpn_v1\.[0-9a-f-]{36}\.[A-Za-z0-9_-]{43}$ ]]

CONTAINER="ovpn-api-smoke-$RANDOM-$$"
docker run -d --name "$CONTAINER" \
  -v "$WORK_DIR/data:/etc/openvpn" \
  -v "$WORK_DIR/config:/etc/ovpn-conf" \
  -e OVPN_API_LISTEN=0.0.0.0:11940 \
  -p 127.0.0.1::11940 \
  --entrypoint ovpn-api \
  "$IMAGE" >/dev/null
api_port="$(docker port "$CONTAINER" 11940/tcp | sed -n '1s/.*://p')"
test -n "$api_port"
api_url="http://127.0.0.1:$api_port"

ready=0
for _ in $(seq 1 50); do
  if curl -fsS "$api_url/healthz" >"$WORK_DIR/health.json" 2>/dev/null; then
    ready=1
    break
  fi
  sleep 0.1
done
if [ "$ready" != 1 ]; then
  docker logs "$CONTAINER" >&2
  exit 1
fi
grep -Fq '"status":"ok"' "$WORK_DIR/health.json"

unauthorized_code="$(curl -sS -o "$WORK_DIR/unauthorized.json" -w '%{http_code}' "$api_url/api/v1/state")"
test "$unauthorized_code" = 401
grep -Fq '"kind":"unauthenticated"' "$WORK_DIR/unauthorized.json"

authorization="Authorization: Bearer $api_key"
curl -fsS -H "$authorization" "$api_url/api/v1/version" >"$WORK_DIR/version.json"
curl -fsS -H "$authorization" "$api_url/api/v1/state" >"$WORK_DIR/state.json"
curl -fsS -D "$WORK_DIR/desired.headers" -H "$authorization" "$api_url/api/v1/config/desired" >"$WORK_DIR/desired.json"
curl -fsS -H "$authorization" "$api_url/api/v1/config/plan" >"$WORK_DIR/plan.json"
grep -Fq '"data_schema":4' "$WORK_DIR/version.json"
grep -Fq '"state":"HEALTHY"' "$WORK_DIR/state.json"
grep -Fq '"digest":' "$WORK_DIR/desired.json"
grep -Eiq '^etag: "[0-9a-f]{64}"' "$WORK_DIR/desired.headers"
grep -Fq '"in_sync":true' "$WORK_DIR/plan.json"

create_code="$(curl -sS -D "$WORK_DIR/create.headers" -o "$WORK_DIR/create.json" -w '%{http_code}' \
  -H "$authorization" -H 'Content-Type: application/json' \
  -d '{"name":"api-client","ipv4":"auto"}' \
  "$api_url/api/v1/clients")"
test "$create_code" = 201
client_id="$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$WORK_DIR/create.json")"
[[ "$client_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]
grep -Eiq "^location: /api/v1/clients/$client_id" "$WORK_DIR/create.headers"

curl -fsS -H "$authorization" "$api_url/api/v1/clients/$client_id" >"$WORK_DIR/client.json"
curl -fsS -D "$WORK_DIR/profile.headers" -H "$authorization" "$api_url/api/v1/clients/$client_id/profile" >"$WORK_DIR/client.ovpn"
grep -Fq '"name":"api-client"' "$WORK_DIR/client.json"
grep -Fq "# ovpn-client-id: $client_id" "$WORK_DIR/client.ovpn"
grep -Eiq '^content-type: application/x-openvpn-profile' "$WORK_DIR/profile.headers"

disconnect_code="$(curl -sS -o "$WORK_DIR/disconnect.json" -w '%{http_code}' \
  -X POST -H "$authorization" "$api_url/api/v1/clients/$client_id/disconnect")"
test "$disconnect_code" = 503
grep -Fq '"kind":"runtime_unavailable"' "$WORK_DIR/disconnect.json"

curl -fsS -X PUT -H "$authorization" -H 'Content-Type: application/json' \
  -d '{"mode":"static","address":"10.71.0.20"}' \
  "$api_url/api/v1/clients/$client_id/ipv4" >"$WORK_DIR/ipv4.json"
grep -Fq '"address":"10.71.0.20"' "$WORK_DIR/ipv4.json"

curl -fsS -X POST -H "$authorization" -H 'Content-Type: application/json' \
  -d '{"release_ipv4":true}' \
  "$api_url/api/v1/clients/$client_id/revoke" >"$WORK_DIR/revoke.json"
curl -fsS -X DELETE -H "$authorization" \
  "$api_url/api/v1/clients/$client_id" >"$WORK_DIR/delete.json"
grep -Fq '"status":"deleted"' "$WORK_DIR/delete.json"

run_ovpn api key delete frontend-smoke --yes --json >"$WORK_DIR/key-delete.json"
deleted_code="$(curl -sS -o "$WORK_DIR/deleted-key.json" -w '%{http_code}' \
  -H "$authorization" "$api_url/api/v1/state")"
test "$deleted_code" = 401

printf 'REST API smoke passed (client=%s)\n' "$client_id"
