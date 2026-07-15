#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${OVPN_MAINTENANCE_IMAGE:-szcq/openvpn-server:maintenance-smoke}"
REQUIRED="${OVPN_MAINTENANCE_REQUIRED:-0}"
SKIP_BUILD="${OVPN_MAINTENANCE_SKIP_BUILD:-0}"
NETWORK="10.88.0.0/24"
RUNTIME_ROOTFS="${OVPN_MAINTENANCE_RUNTIME_ROOTFS:-}"
runtime_mounts=()
WORK_DIR=''
container_name="ovpn-maintenance-$$-$(date +%s)"

skip_or_fail() {
  local reason="$1"

  if [ "$REQUIRED" = 1 ]; then
    printf 'maintenance container smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'maintenance container smoke skipped: %s\n' "$reason"
  exit 0
}

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
  skip_or_fail 'missing command: docker'
fi
if ! docker info >/dev/null 2>&1; then
  skip_or_fail 'Docker daemon is not accessible'
fi
if [ -n "$RUNTIME_ROOTFS" ]; then
  if [ ! -x "$RUNTIME_ROOTFS/usr/local/bin/ovpn" ] || [ ! -d "$RUNTIME_ROOTFS/usr/local/lib/openvpn-container" ]; then
    skip_or_fail "invalid runtime rootfs: $RUNTIME_ROOTFS"
  fi
  runtime_mounts=(
    -v "$RUNTIME_ROOTFS/usr/local/bin/ovpn:/usr/local/bin/ovpn:ro"
    -v "$RUNTIME_ROOTFS/usr/local/lib/openvpn-container:/usr/local/lib/openvpn-container:ro"
  )
fi
if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-maintenance.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"

run_control() {
  docker run --rm \
    -e OVPN_ENDPOINT=maintenance.example.test \
    -e OVPN_NETWORK="$NETWORK" \
    -e OVPN_PROTO=udp \
    -v "$data_dir:/etc/openvpn" \
    "${runtime_mounts[@]}" \
    "$IMAGE" \
    "$@"
}

run_control init >/tmp/ovpn-maintenance-init.out 2>/tmp/ovpn-maintenance-init.err
docker run --rm -v "$data_dir:/etc/openvpn" --entrypoint /bin/sh "$IMAGE" -ec 'rm /etc/openvpn/pki/index.txt'

set +e
run_control state doctor --json >"$WORK_DIR/doctor.json" 2>"$WORK_DIR/doctor.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq '"state": "CRITICAL"' "$WORK_DIR/doctor.json"
grep -Fq 'PKI_INDEX_MISSING' "$WORK_DIR/doctor.json"

set +e
run_control repair plan >"$WORK_DIR/plan.out" 2>"$WORK_DIR/plan.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq '[BLOCKED] PKI_INDEX_MISSING' "$WORK_DIR/plan.out"

set +e
run_control start >"$WORK_DIR/exit.out" 2>"$WORK_DIR/exit.err"
status=$?
set -e
[ "$status" -eq 78 ]

docker run -d \
  --name "$container_name" \
  -e OVPN_ENDPOINT=maintenance.example.test \
  -e OVPN_NETWORK="$NETWORK" \
  -e OVPN_PROTO=udp \
  -e OVPN_CRITICAL_MODE=maintenance \
  -v "$data_dir:/etc/openvpn" \
  "${runtime_mounts[@]}" \
  "$IMAGE" \
  start >/dev/null

for _ in $(seq 1 30); do
  status_output="$(docker exec "$container_name" ovpn runtime status 2>/dev/null || true)"
  if grep -Fq '"maintenance": true' <<<"$status_output"; then
    break
  fi
  sleep 1
done
grep -Fq '"instance_state": "CRITICAL"' <<<"$status_output"
grep -Fq '"daemon": "stopped"' <<<"$status_output"
grep -Fq '"maintenance": true' <<<"$status_output"
set +e
docker exec "$container_name" ovpn runtime health >"$WORK_DIR/health.out" 2>"$WORK_DIR/health.err"
status=$?
set -e
[ "$status" -eq 1 ]
grep -Fq 'instance is in maintenance mode' "$WORK_DIR/health.err"
if docker exec "$container_name" pgrep -x openvpn >/dev/null 2>&1; then
  echo 'maintenance container unexpectedly started OpenVPN' >&2
  exit 1
fi

for _ in $(seq 1 30); do
  health="$(docker inspect -f '{{.State.Health.Status}}' "$container_name")"
  [ "$health" = unhealthy ] && break
  sleep 1
done
[ "$health" = unhealthy ] || {
  docker logs "$container_name" >&2
  echo "expected unhealthy maintenance container, got $health" >&2
  exit 1
}

printf 'maintenance container smoke passed (network=%s)\n' "$NETWORK"
