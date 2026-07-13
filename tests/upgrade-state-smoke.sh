#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_IMAGE="${OVPN_UPGRADE_SOURCE_IMAGE:-szcq/openvpn-server:upgrade-source}"
TARGET_IMAGE="${OVPN_UPGRADE_TARGET_IMAGE:-szcq/openvpn-server:upgrade-target}"
REQUIRED="${OVPN_UPGRADE_REQUIRED:-0}"
SKIP_BUILD="${OVPN_UPGRADE_SKIP_BUILD:-0}"
NETWORK='10.88.0.0/24'
START_TIMEOUT="${OVPN_UPGRADE_START_TIMEOUT:-20s}"
WORK_DIR=''

skip_or_fail() {
  local reason="$1"

  if [ "$REQUIRED" = 1 ]; then
    printf 'upgrade state smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'upgrade state smoke skipped: %s\n' "$reason"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$TARGET_IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
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
if [ ! -c /dev/net/tun ]; then
  skip_or_fail 'host /dev/net/tun is not available'
fi
if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$TARGET_IMAGE" "$ROOT_DIR"
  SOURCE_IMAGE="$TARGET_IMAGE"
fi
if ! docker image inspect "$SOURCE_IMAGE" >/dev/null 2>&1; then
  skip_or_fail "source image not found: $SOURCE_IMAGE"
fi
if ! docker image inspect "$TARGET_IMAGE" >/dev/null 2>&1; then
  skip_or_fail "target image not found: $TARGET_IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-upgrade.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"

docker run --rm \
  -e OVPN_ENDPOINT=upgrade.example.test \
  -e "OVPN_NETWORK=$NETWORK" \
  -e OVPN_PROTO=udp \
  -v "$data_dir:/etc/openvpn" \
  "$SOURCE_IMAGE" \
  ovpn init >"$WORK_DIR/source-init.out" 2>"$WORK_DIR/source-init.err"

test "$(docker run --rm -v "$data_dir:/etc/openvpn" "$TARGET_IMAGE" ovpn state)" = HEALTHY

auto_start_log="$WORK_DIR/target-start.log"
set +e
docker run --rm \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e OVPN_ENDPOINT=upgrade.example.test \
  -e "OVPN_NETWORK=$NETWORK" \
  -e OVPN_PROTO=udp \
  -v "$data_dir:/etc/openvpn" \
  --entrypoint /usr/bin/timeout \
  "$TARGET_IMAGE" \
  "$START_TIMEOUT" ovpn start >"$auto_start_log" 2>&1
status=$?
set -e
[ "$status" -eq 124 ] || {
  cat "$auto_start_log" >&2
  echo "target start returned $status instead of timeout" >&2
  exit 1
}
grep -Fq 'Initialization Sequence Completed' "$auto_start_log"

printf 'upgrade state smoke passed (network=%s source=%s target=%s)\n' "$NETWORK" "$SOURCE_IMAGE" "$TARGET_IMAGE"
