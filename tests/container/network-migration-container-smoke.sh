#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${OVPN_NETWORK_MIGRATION_IMAGE:-szcq/openvpn-server:network-migration-smoke}"
REQUIRED="${OVPN_NETWORK_MIGRATION_REQUIRED:-0}"
SKIP_BUILD="${OVPN_NETWORK_MIGRATION_SKIP_BUILD:-0}"
NETWORK='10.88.0.0/24'
MIGRATED_NETWORK='10.89.0.0/24'
FAILED_NETWORK='10.90.0.0/24'
RUN_ID="ovpn-network-migration-$$-$(date +%s)"
WORK_DIR=''
CONTAINER_NAME="$RUN_ID-server"
NETWORK_NAME="$RUN_ID-net"

skip_or_fail() {
  local reason="$1"

  if [ "$REQUIRED" = 1 ]; then
    printf 'network migration container smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'network migration container smoke skipped: %s\n' "$reason"
  exit 0
}

cleanup() {
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

wait_for_health() {
  local deadline=$((SECONDS + 30))

  while [ "$SECONDS" -lt "$deadline" ]; do
    if docker exec "$CONTAINER_NAME" ovpn runtime health >/dev/null 2>&1; then
      return 0
    fi
    if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
      docker logs "$CONTAINER_NAME" >&2 || true
      echo 'OpenVPN container exited during migration' >&2
      exit 1
    fi
    sleep 1
  done
  docker logs "$CONTAINER_NAME" >&2 || true
  echo 'OpenVPN did not become healthy before timeout' >&2
  exit 1
}

if ! command -v docker >/dev/null 2>&1; then
  skip_or_fail 'missing command: docker'
fi
if ! docker info >/dev/null 2>&1; then
  skip_or_fail 'Docker daemon is not accessible'
fi
if [ ! -c /dev/net/tun ]; then
  skip_or_fail 'host /dev/net/tun is not available'
fi
if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-network-migration.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"
docker network create "$NETWORK_NAME" >/dev/null
docker run -d \
  --name "$CONTAINER_NAME" \
  --network "$NETWORK_NAME" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e OVPN_ENDPOINT=network-migration.example.test \
  -e OVPN_NETWORK="$NETWORK" \
  -e OVPN_NAT=false \
  -e OVPN_DYNAMIC_POOL_SIZE=64 \
  -v "$data_dir:/etc/openvpn" \
  "$IMAGE" \
  ovpn start >/dev/null

wait_for_health
docker exec "$CONTAINER_NAME" ovpn client create static-client >/dev/null
docker exec "$CONTAINER_NAME" ovpn client create dynamic-client --dynamic >/dev/null
docker exec "$CONTAINER_NAME" ovpn network apply --network "$MIGRATED_NETWORK" --dynamic-pool-size 100 --yes >/tmp/ovpn-network-migration-apply.out
wait_for_health
docker exec "$CONTAINER_NAME" ovpn runtime health
test "$(docker exec "$CONTAINER_NAME" ovpn state show)" = HEALTHY
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec '
  grep -Fqx "OVPN_NETWORK=$1" /etc/openvpn/config/project.env
  grep -Fqx "static-client,10.89.0.2" /etc/openvpn/data/client-ip.csv
  grep -Fqx "ifconfig-push 10.89.0.2 255.255.255.0" /etc/openvpn/ccd/static-client
  grep -Fq '"'"'"event":"network_migration","outcome":"applied"'"'"' /etc/openvpn/meta/audit.jsonl
' sh "$MIGRATED_NETWORK"

if docker exec -e OVPN_NETWORK_MIGRATION_FAIL_HEALTH=true "$CONTAINER_NAME" ovpn network apply --network "$FAILED_NETWORK" --yes >/tmp/ovpn-network-migration-fail.out 2>/tmp/ovpn-network-migration-fail.err; then
  echo 'injected network migration health failure unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'network migration health check failed; rollback completed' /tmp/ovpn-network-migration-fail.err
wait_for_health
docker exec "$CONTAINER_NAME" ovpn runtime health
test "$(docker exec "$CONTAINER_NAME" ovpn state show)" = HEALTHY
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec '
  grep -Fqx "OVPN_NETWORK=$1" /etc/openvpn/config/project.env
  grep -Fqx "static-client,10.89.0.2" /etc/openvpn/data/client-ip.csv
  grep -Fq '"'"'"event":"network_migration","outcome":"rejected"'"'"' /etc/openvpn/meta/audit.jsonl
' sh "$MIGRATED_NETWORK"

printf 'network migration container smoke passed (network=%s)\n' "$MIGRATED_NETWORK"
