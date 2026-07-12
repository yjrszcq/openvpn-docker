#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OVPN_REPAIR_IMAGE:-szcq/openvpn-server:repair-smoke}"
REQUIRED="${OVPN_REPAIR_REQUIRED:-0}"
SKIP_BUILD="${OVPN_REPAIR_SKIP_BUILD:-0}"
NETWORK="10.88.0.0/24"
RUNTIME_ROOTFS="${OVPN_REPAIR_RUNTIME_ROOTFS:-}"
runtime_mounts=()
WORK_DIR=""

skip_or_fail() {
  local reason="$1"
  if [ "$REQUIRED" = 1 ]; then
    printf 'repair container smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'repair container smoke skipped: %s\n' "$reason"
  exit 0
}

cleanup() {
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
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-repair.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"

run_control() {
  docker run --rm \
    -e OVPN_ENDPOINT=repair.example.test \
    -e OVPN_NETWORK="$NETWORK" \
    -e OVPN_PROTO=udp \
    -v "$data_dir:/etc/openvpn" \
    "${runtime_mounts[@]}" \
    "$IMAGE" \
    "$@"
}

run_control init >/tmp/ovpn-repair-init.out 2>/tmp/ovpn-repair-init.err
run_control add-client repair-client >/tmp/ovpn-repair-add.out 2>/tmp/ovpn-repair-add.err
identity_before="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec 'sha256sum /etc/openvpn/pki/ca.crt /etc/openvpn/pki/private/ca.key /etc/openvpn/pki/issued/openvpn-server.crt /etc/openvpn/pki/private/openvpn-server.key /etc/openvpn/pki/issued/repair-client.crt /etc/openvpn/pki/private/repair-client.key')"
docker run --rm -v "$data_dir:/etc/openvpn" --entrypoint /bin/sh "$IMAGE" -ec 'rm /etc/openvpn/config/schema-version /etc/openvpn/meta/instance.json /etc/openvpn/server/server.conf /etc/openvpn/pki/crl.pem /etc/openvpn/clients/active/repair-client.ovpn'
run_control repair >/tmp/ovpn-repair.out 2>/tmp/ovpn-repair.err
[ "$(run_control state)" = HEALTHY ]
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec 'test -f /etc/openvpn/config/schema-version && test -f /etc/openvpn/meta/instance.json && test -f /etc/openvpn/server/server.conf && test -f /etc/openvpn/pki/crl.pem && test -f /etc/openvpn/clients/active/repair-client.ovpn'
identity_after="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec 'sha256sum /etc/openvpn/pki/ca.crt /etc/openvpn/pki/private/ca.key /etc/openvpn/pki/issued/openvpn-server.crt /etc/openvpn/pki/private/openvpn-server.key /etc/openvpn/pki/issued/repair-client.crt /etc/openvpn/pki/private/repair-client.key')"
[ "$identity_before" = "$identity_after" ] || {
  echo 'container repair changed identity material' >&2
  exit 1
}

docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec "grep -Fq '\"result\": \"success\"' /etc/openvpn/repair/journal/*.json"
printf 'repair container smoke passed (network=%s)\n' "$NETWORK"
