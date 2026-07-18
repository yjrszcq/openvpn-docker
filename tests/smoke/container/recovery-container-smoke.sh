#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_RECOVERY_IMAGE:-szcq/openvpn-server:recovery-smoke}"
REQUIRED="${OVPN_RECOVERY_REQUIRED:-0}"
SKIP_BUILD="${OVPN_RECOVERY_SKIP_BUILD:-0}"
NETWORK="10.88.0.0/24"
RUNTIME_ROOTFS="${OVPN_RECOVERY_RUNTIME_ROOTFS:-}"
runtime_mounts=()
WORK_DIR=""

skip_or_fail() {
  local reason="$1"

  if [ "$REQUIRED" = 1 ]; then
    printf 'recovery container smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'recovery container smoke skipped: %s\n' "$reason"
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
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-recovery.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"

run_control() {
  docker run --rm \
    -e OVPN_ENDPOINT=recovery.example.test \
    -e OVPN_NETWORK="$NETWORK" \
    -e OVPN_PROTO=udp \
    -v "$data_dir:/etc/openvpn" \
    "${runtime_mounts[@]}" \
    "$IMAGE" \
    "$@"
}

run_control init >/tmp/ovpn-recovery-init.out 2>/tmp/ovpn-recovery-init.err
run_control client create recovery-client >/tmp/ovpn-recovery-add.out 2>/tmp/ovpn-recovery-add.err
client_id="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/awk "$IMAGE" -F, '$2 == "recovery-client" { print $1 }' /etc/openvpn/meta/client-state.csv)"
[[ "$client_id" =~ ^[0-9a-f-]{36}$ ]]
identity_before="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec "sha256sum /etc/openvpn/pki/ca.crt /etc/openvpn/secrets/tls-crypt.key /etc/openvpn/pki/issued/$client_id.crt /etc/openvpn/pki/private/$client_id.key")"
docker run --rm -v "$data_dir:/etc/openvpn" --entrypoint /bin/sh "$IMAGE" -ec "rm /etc/openvpn/pki/ca.crt /etc/openvpn/secrets/tls-crypt.key /etc/openvpn/pki/issued/$client_id.crt /etc/openvpn/pki/private/$client_id.key"
recovery_state="$(run_control state show)"
if [ "$recovery_state" != DEGRADED_RECOVERABLE ]; then
  run_control state doctor --json >&2 || true
  printf 'expected DEGRADED_RECOVERABLE after identity loss, got %s\n' "$recovery_state" >&2
  exit 1
fi
run_control repair plan >/tmp/ovpn-recovery-plan.out 2>/tmp/ovpn-recovery-plan.err
grep -Fq '[RECOVER] RECOVER_CA_CERT' /tmp/ovpn-recovery-plan.out
grep -Fq '[RECOVER] RECOVER_TLS_CRYPT_KEY' /tmp/ovpn-recovery-plan.out
grep -Fq '[RECOVER] RECOVER_CLIENT_CERT' /tmp/ovpn-recovery-plan.out
grep -Fq '[RECOVER] RECOVER_CLIENT_KEY' /tmp/ovpn-recovery-plan.out
run_control repair apply >/tmp/ovpn-recovery-repair.out 2>/tmp/ovpn-recovery-repair.err
[ "$(run_control state show)" = HEALTHY ]
identity_after="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec "sha256sum /etc/openvpn/pki/ca.crt /etc/openvpn/secrets/tls-crypt.key /etc/openvpn/pki/issued/$client_id.crt /etc/openvpn/pki/private/$client_id.key")"
[ "$identity_before" = "$identity_after" ] || {
  echo 'container recovery changed identity material' >&2
  exit 1
}
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec "test \"\$(stat -c %a /etc/openvpn/pki/ca.crt)\" = 644 && test \"\$(stat -c %a /etc/openvpn/secrets/tls-crypt.key)\" = 600 && test \"\$(stat -c %a /etc/openvpn/pki/issued/$client_id.crt)\" = 644 && test \"\$(stat -c %a /etc/openvpn/pki/private/$client_id.key)\" = 600"
printf 'recovery container smoke passed (network=%s)\n' "$NETWORK"
