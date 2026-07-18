#!/usr/bin/env bash
set -euo pipefail

IMAGE="${OVPN_MIGRATION_LOCK_IMAGE:-szcq/openvpn-server:migration-lock-smoke}"
REQUIRED="${OVPN_MIGRATION_LOCK_REQUIRED:-0}"
SKIP_BUILD="${OVPN_MIGRATION_LOCK_SKIP_BUILD:-0}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK_DIR=''
CONTAINER="ovpn-migration-lock-$$"

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'migration lock container smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'migration lock container smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'
[ -c /dev/net/tun ] || skip_or_fail 'host /dev/net/tun is not available'
if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-migration-lock.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"
docker run --rm \
  -e OVPN_ENDPOINT=lock.example.test \
  -e OVPN_NETWORK=10.88.0.0/24 \
  -v "$data_dir:/etc/openvpn" \
  "$IMAGE" init >/tmp/ovpn-migration-lock-init.out 2>/tmp/ovpn-migration-lock-init.err

docker run -d \
  --name "$CONTAINER" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -e OVPN_ENDPOINT=lock.example.test \
  -e OVPN_NETWORK=10.88.0.0/24 \
  -v "$data_dir:/etc/openvpn" \
  "$IMAGE" >/dev/null
for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" ovpn runtime health >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
docker exec "$CONTAINER" ovpn runtime health

set +e
docker run --rm \
  -e OVPN_DATA_DIR=/etc/openvpn \
  -v "$data_dir:/etc/openvpn" \
  --entrypoint /bin/bash \
  "$IMAGE" -ec \
  '. /usr/local/lib/openvpn-container/common.sh
   . /usr/local/lib/openvpn-container/lock.sh
   ovpn_with_runtime_exclusive_lock true' \
  >/tmp/ovpn-migration-lock-live.out 2>/tmp/ovpn-migration-lock-live.err
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'stop the openvpn service before migration' /tmp/ovpn-migration-lock-live.err

docker stop -t 10 "$CONTAINER" >/dev/null
docker rm "$CONTAINER" >/dev/null
docker run --rm \
  -e OVPN_DATA_DIR=/etc/openvpn \
  -v "$data_dir:/etc/openvpn" \
  --entrypoint /bin/bash \
  "$IMAGE" -ec \
  '. /usr/local/lib/openvpn-container/common.sh
   . /usr/local/lib/openvpn-container/lock.sh
   ovpn_with_runtime_exclusive_lock true'

printf 'migration lock container smoke passed\n'
