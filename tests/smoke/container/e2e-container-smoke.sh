#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_E2E_IMAGE:-szcq/openvpn-server:e2e-smoke}"
REQUIRED="${OVPN_E2E_REQUIRED:-0}"
SKIP_BUILD="${OVPN_E2E_SKIP_BUILD:-0}"
WORK_DIR=''
active_containers=()
active_networks=()

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'E2E smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'E2E smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  for container in "${active_containers[@]}"; do
    docker rm -f "$container" >/dev/null 2>&1 || true
  done
  for network in "${active_networks[@]}"; do
    docker network rm "$network" >/dev/null 2>&1 || true
  done
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rmdir "$WORK_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'
[ -c /dev/net/tun ] || skip_or_fail 'host /dev/net/tun is not available'

if [ "$SKIP_BUILD" != 1 ]; then
  OVPN_BUILD_NETWORK=host "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-e2e.XXXXXX")"

run_protocol() {
  local protocol="$1"
  local index="$2"
  local protocol_dir="$WORK_DIR/$protocol"
  local data_dir="$protocol_dir/data"
  local config_dir="$protocol_dir/config"
  local server="ovpn-e2e-$protocol-$$"
  local network="ovpn-e2e-$protocol-net-$$"
  local client="e2e-$protocol"

  mkdir -p "$protocol_dir"
  mkdir -m 0750 "$data_dir" "$config_dir"
  cat >"$config_dir/config.yaml" <<YAML
version: 1
server:
  endpoint: $server
  transport:
    protocol: $protocol
    family: auto
    port: 1194
  clientToClient: true
ipv4:
  network: 10.$((100 + index)).0.0/24
  dynamicPoolSize: 64
  nat:
    enabled: false
    interface: auto
  redirectGateway: false
  dns: []
  routes: []
logging:
  maxBytes: 10485760
  backups: 5
YAML

  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/openvpn-config" \
    --entrypoint ovpn \
    "$IMAGE" server init >"$protocol_dir/init.out"
  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/openvpn-config" \
    --entrypoint ovpn \
    "$IMAGE" client create "$client" --ipv4 dynamic >"$protocol_dir/create.out"
  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/openvpn-config" \
    --entrypoint ovpn \
    "$IMAGE" client export --name "$client" --output - >"$protocol_dir/client.ovpn"

  docker network create "$network" >/dev/null
  active_networks+=("$network")
  docker run -d \
    --name "$server" \
    --network "$network" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun \
    -e OVPN_IPTABLES_BIN=iptables-legacy \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/openvpn-config:ro" \
    "$IMAGE" >"$protocol_dir/server.id"
  active_containers+=("$server")

  for _ in $(seq 1 60); do
    if docker exec "$server" ovpn runtime health >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
  if ! docker exec "$server" ovpn runtime health; then
    docker logs "$server" >&2 || true
    return 1
  fi

  set +e
  docker run --rm \
    --network "$network" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun \
    -v "$protocol_dir/client.ovpn:/client.ovpn:ro" \
    --entrypoint /usr/bin/timeout \
    "$IMAGE" 20s openvpn --config /client.ovpn >"$protocol_dir/client.log" 2>&1
  local status=$?
  set -e
  if [ "$status" -ne 124 ] || ! grep -Fq 'Initialization Sequence Completed' "$protocol_dir/client.log"; then
    cat "$protocol_dir/client.log" >&2
    docker logs "$server" >&2 || true
    return 1
  fi

  docker rm -f "$server" >/dev/null
  docker network rm "$network" >/dev/null
}

run_protocol udp 1
run_protocol tcp 2
printf 'Go UDP/TCP E2E smoke passed\n'
