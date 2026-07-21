#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SOURCE_IMAGE="${OVPN_GO_HANDOFF_SOURCE_IMAGE:-szcq/openvpn-server:sh-ver-handoff}"
GO_IMAGE="${OVPN_GO_HANDOFF_GO_IMAGE:-golang:1.26.5-trixie}"
GO_CACHE_DIR="${OVPN_GO_HANDOFF_CACHE_DIR:-${TMPDIR:-/tmp}/ovpn-go-handoff-cache}"
REQUIRED="${OVPN_GO_HANDOFF_REQUIRED:-0}"
SKIP_SOURCE_BUILD="${OVPN_GO_HANDOFF_SKIP_SOURCE_BUILD:-0}"
SKIP_GO_BUILD="${OVPN_GO_HANDOFF_SKIP_GO_BUILD:-0}"
WORK_DIR=''
NETWORK_NAME="ovpn-go-handoff-$$"
SERVER_CONTAINER="ovpn-go-handoff-server-$$"
CLIENT_NAME='handoff-client'

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'Go SQLite handoff smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'Go SQLite handoff smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  docker rm -f "$SERVER_CONTAINER" >/dev/null 2>&1 || true
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$SOURCE_IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'
[ -c /dev/net/tun ] || skip_or_fail 'host /dev/net/tun is not available'

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-go-handoff.XXXXXX")"
source_context="$WORK_DIR/sh-ver"
data_dir="$WORK_DIR/data"
config_dir="$WORK_DIR/config"
bin_dir="$WORK_DIR/bin"
mkdir -p "$source_context" "$data_dir" "$config_dir" "$bin_dir"
chmod 750 "$data_dir" "$config_dir"

if [ "$SKIP_SOURCE_BUILD" != 1 ]; then
  git -C "$ROOT_DIR" archive sh-ver | tar -x -C "$source_context"
  OVPN_BUILD_NETWORK=host \
    OVPN_BUILD_HTTP_PROXY="${http_proxy-${HTTP_PROXY-}}" \
    OVPN_BUILD_HTTPS_PROXY="${https_proxy-${HTTPS_PROXY-}}" \
    OVPN_BUILD_NO_PROXY="${no_proxy-${NO_PROXY-}}" \
    "$source_context/scripts/docker-build.sh" -t "$SOURCE_IMAGE" "$source_context"
elif ! docker image inspect "$SOURCE_IMAGE" >/dev/null 2>&1; then
  skip_or_fail "source image not found: $SOURCE_IMAGE"
fi

if [ "$SKIP_GO_BUILD" != 1 ]; then
  proxy_args=()
  mkdir -p "$GO_CACHE_DIR/build" "$GO_CACHE_DIR/mod"
  for name in http_proxy https_proxy no_proxy HTTP_PROXY HTTPS_PROXY NO_PROXY; do
    if [ -n "${!name:-}" ]; then
      proxy_args+=(-e "$name=${!name}")
    fi
  done
  docker run --rm --network host \
    --user "$(id -u):$(id -g)" \
    "${proxy_args[@]}" \
    -e CGO_ENABLED=1 \
    -e GOPROXY=direct \
    -e GOCACHE=/cache/build \
    -e GOMODCACHE=/cache/mod \
    -v "$ROOT_DIR:/src:ro" \
    -v "$bin_dir:/out" \
    -v "$GO_CACHE_DIR:/cache" \
    -w /src \
    "$GO_IMAGE" \
    sh -ec 'go build -buildvcs=false -o /out/ovpn ./cmd/ovpn && go build -buildvcs=false -o /out/ovpn-broker ./cmd/ovpn-broker'
fi
test -x "$bin_dir/ovpn"
test -x "$bin_dir/ovpn-broker"
cp "$ROOT_DIR/compatibility/contract.json" "$bin_dir/contract.json"

run_shell() {
  docker run --rm \
    -e OVPN_ENDPOINT="$SERVER_CONTAINER" \
    -e OVPN_NETWORK=10.98.0.0/24 \
    -e OVPN_PROTO=udp \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/openvpn-config" \
    "$SOURCE_IMAGE" "$@"
}

run_go() {
  docker run --rm \
    -e OVPN_DATA_DIR=/etc/openvpn \
    -e OVPN_RUNTIME_DIR=/run/openvpn-container \
    -e OVPN_COMPATIBILITY_FILE=/tool/contract.json \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/openvpn-config" \
    -v "$bin_dir:/tool:ro" \
    --entrypoint /tool/ovpn \
    "$SOURCE_IMAGE" "$@"
}

run_shell ovpn init >"$WORK_DIR/init.out"
run_shell ovpn client create "$CLIENT_NAME" --dynamic >"$WORK_DIR/create.out"
client_id="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/awk "$SOURCE_IMAGE" -F, -v name="$CLIENT_NAME" '$2 == name && $3 == "active" { print $1 }' /etc/openvpn/meta/client-state.csv)"
[[ "$client_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]
run_shell ovpn client export "$CLIENT_NAME" >"$WORK_DIR/schema3.ovpn"
certificate_before="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$SOURCE_IMAGE" "/etc/openvpn/pki/issued/$client_id.crt")"

run_go migrate plan --json >"$WORK_DIR/plan.json"
grep -Fq '"status":"ready"' "$WORK_DIR/plan.json"
grep -Fq '"source_schema":3' "$WORK_DIR/plan.json"
docker run --rm \
  -e OVPN_DATA_DIR=/etc/openvpn \
  -e OVPN_RUNTIME_DIR=/run/openvpn-container \
  -e OVPN_MAINTENANCE=true \
  -e OVPN_COMPATIBILITY_FILE=/tool/contract.json \
  -v "$data_dir:/etc/openvpn" \
  -v "$config_dir:/etc/openvpn-config" \
  -v "$bin_dir:/tool:ro" \
  --entrypoint /tool/ovpn \
  "$SOURCE_IMAGE" migrate apply --yes --json >"$WORK_DIR/apply.json"
grep -Fq '"applied":true' "$WORK_DIR/apply.json"
grep -Fq '"final_state":"HEALTHY"' "$WORK_DIR/apply.json"
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$SOURCE_IMAGE" -ec 'test -f /etc/openvpn/meta/state.db && test ! -e /etc/openvpn/meta/client-state.csv'
test "$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$SOURCE_IMAGE" "/etc/openvpn/pki/issued/$client_id.crt")" = "$certificate_before"
run_go state doctor --json >"$WORK_DIR/doctor-before-export.json"
grep -Fq '"state":"DEGRADED_REPAIRABLE"' "$WORK_DIR/doctor-before-export.json"
grep -Fq '"id":"DECLARATIVE_CONFIG_UNAVAILABLE"' "$WORK_DIR/doctor-before-export.json"
run_go config export --output /etc/openvpn-config/config.yaml
run_go state doctor --json >"$WORK_DIR/doctor.json"
grep -Fq '"state":"HEALTHY"' "$WORK_DIR/doctor.json"
run_go client export --name "$CLIENT_NAME" --output - >"$WORK_DIR/schema4.ovpn"

docker network create "$NETWORK_NAME" >/dev/null
docker run -d \
  --name "$SERVER_CONTAINER" \
  --network "$NETWORK_NAME" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -e OVPN_DATA_DIR=/etc/openvpn \
  -e OVPN_RUNTIME_DIR=/run/openvpn-container \
  -e OVPN_BROKER_BIN=/tool/ovpn-broker \
  -e OVPN_IPTABLES_BIN=iptables-legacy \
  -v "$data_dir:/etc/openvpn" \
  -v "$config_dir:/etc/openvpn-config" \
  -v "$bin_dir:/tool:ro" \
  -v "$bin_dir/ovpn:/usr/local/bin/ovpn-hook:ro" \
  --entrypoint /tool/ovpn \
  "$SOURCE_IMAGE" server run >"$WORK_DIR/go-server.id"
for _ in $(seq 1 60); do
  if docker exec "$SERVER_CONTAINER" /tool/ovpn runtime health >/dev/null 2>&1; then break; fi
  sleep 0.5
done
if ! docker exec "$SERVER_CONTAINER" /tool/ovpn runtime health; then
  docker logs "$SERVER_CONTAINER" >"$WORK_DIR/go-server.log" 2>&1 || true
  cat "$WORK_DIR/go-server.log" >&2
  exit 1
fi
set +e
docker run --rm \
  --network "$NETWORK_NAME" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v "$WORK_DIR/schema4.ovpn:/client.ovpn:ro" \
  --entrypoint /usr/bin/timeout \
  "$SOURCE_IMAGE" 20s openvpn --config /client.ovpn >"$WORK_DIR/go-client.log" 2>&1
client_status=$?
set -e
if [ "$client_status" -ne 124 ] || ! grep -Fq 'Initialization Sequence Completed' "$WORK_DIR/go-client.log"; then
	cat "$WORK_DIR/go-client.log" >&2
	docker logs "$SERVER_CONTAINER" >&2 || true
  printf 'Go client returned %s\n' "$client_status" >&2
  exit 1
fi
docker stop -t 10 "$SERVER_CONTAINER" >/dev/null
docker rm "$SERVER_CONTAINER" >/dev/null

docker run --rm \
  -v "$data_dir:/etc/openvpn" \
  --entrypoint /bin/bash \
  "$SOURCE_IMAGE" -ec '
    cd /etc/openvpn/repair/migrations
    sha256sum -c schema3-pre-v4.tar.gz.sha256
    cd /etc/openvpn
    rm -rf cache ccd clients config data meta pki secrets server
    tar -xzf repair/migrations/schema3-pre-v4.tar.gz -C /etc/openvpn
  '
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$SOURCE_IMAGE" -ec 'test ! -e /etc/openvpn/meta/state.db && test -f /etc/openvpn/meta/client-state.csv'
test "$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$SOURCE_IMAGE" "/etc/openvpn/pki/issued/$client_id.crt")" = "$certificate_before"
test "$(run_shell ovpn state show)" = HEALTHY

docker run -d \
  --name "$SERVER_CONTAINER" \
  --network "$NETWORK_NAME" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v "$data_dir:/etc/openvpn" \
  "$SOURCE_IMAGE" >"$WORK_DIR/shell-server.id"
for _ in $(seq 1 60); do
  if docker exec "$SERVER_CONTAINER" ovpn runtime health >/dev/null 2>&1; then break; fi
  sleep 0.5
done
if ! docker exec "$SERVER_CONTAINER" ovpn runtime health; then
  docker logs "$SERVER_CONTAINER" >"$WORK_DIR/shell-server.log" 2>&1 || true
  cat "$WORK_DIR/shell-server.log" >&2
  exit 1
fi
set +e
docker run --rm \
  --network "$NETWORK_NAME" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v "$WORK_DIR/schema3.ovpn:/client.ovpn:ro" \
  --entrypoint /usr/bin/timeout \
  "$SOURCE_IMAGE" 20s openvpn --config /client.ovpn >"$WORK_DIR/shell-client.log" 2>&1
client_status=$?
set -e
if [ "$client_status" -ne 124 ] || ! grep -Fq 'Initialization Sequence Completed' "$WORK_DIR/shell-client.log"; then
  cat "$WORK_DIR/shell-client.log" >&2
  printf 'Shell client returned %s\n' "$client_status" >&2
  exit 1
fi

printf 'Go SQLite handoff smoke passed (client=%s)\n' "$client_id"
