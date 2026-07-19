#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SOURCE_IMAGE="${OVPN_HANDOFF_SOURCE_IMAGE:-szcq/openvpn-server:handoff-source}"
TARGET_IMAGE="${OVPN_HANDOFF_TARGET_IMAGE:-szcq/openvpn-server:handoff-target}"
REQUIRED="${OVPN_HANDOFF_REQUIRED:-0}"
SKIP_BUILD="${OVPN_HANDOFF_SKIP_BUILD:-0}"
NETWORK='10.88.0.0/24'
START_TIMEOUT="${OVPN_HANDOFF_START_TIMEOUT:-20s}"
WORK_DIR=''

skip_or_fail() {
  local reason="$1"

  if [ "$REQUIRED" = 1 ]; then
    printf 'image handoff smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'image handoff smoke skipped: %s\n' "$reason"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$TARGET_IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

image_schema() {
  local image="$1"
  docker run --rm --entrypoint ovpn "$image" runtime version |
    sed -n 's/.*"data_schema": *\([0-9][0-9]*\).*/\1/p' |
    head -1
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
  "$ROOT_DIR/scripts/docker-build.sh" -t "$TARGET_IMAGE" "$ROOT_DIR"
  SOURCE_IMAGE="$TARGET_IMAGE"
fi
if ! docker image inspect "$SOURCE_IMAGE" >/dev/null 2>&1; then
  skip_or_fail "source image not found: $SOURCE_IMAGE"
fi
if ! docker image inspect "$TARGET_IMAGE" >/dev/null 2>&1; then
  skip_or_fail "target image not found: $TARGET_IMAGE"
fi

source_schema="$(image_schema "$SOURCE_IMAGE")"
target_schema="$(image_schema "$TARGET_IMAGE")"
[[ "$source_schema" =~ ^[1-9][0-9]*$ ]] || skip_or_fail 'source image has no valid data schema'
[[ "$target_schema" =~ ^[1-9][0-9]*$ ]] || skip_or_fail 'target image has no valid data schema'
if [ "$source_schema" -gt "$target_schema" ]; then
  skip_or_fail "target schema $target_schema is older than source schema $source_schema"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-image-handoff.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"

docker run --rm \
  -e OVPN_ENDPOINT=handoff.example.test \
  -e "OVPN_NETWORK=$NETWORK" \
  -e OVPN_PROTO=udp \
  -v "$data_dir:/etc/openvpn" \
  "$SOURCE_IMAGE" \
  ovpn init >"$WORK_DIR/source-init.out" 2>"$WORK_DIR/source-init.err"

if [ "$source_schema" -eq "$target_schema" ]; then
  test "$(docker run --rm -v "$data_dir:/etc/openvpn" "$TARGET_IMAGE" ovpn state show)" = HEALTHY
else
  set +e
  docker run --rm -v "$data_dir:/etc/openvpn" "$TARGET_IMAGE" ovpn state show \
    >"$WORK_DIR/rejected.out" 2>"$WORK_DIR/rejected.err"
  status=$?
  set -e
  [ "$status" -eq 78 ]
  grep -Fq 'openvpn-maintenance migrate plan' "$WORK_DIR/rejected.err"

  docker run --rm \
    -e OVPN_MAINTENANCE=true \
    -v "$data_dir:/etc/openvpn" \
    "$TARGET_IMAGE" migrate plan --json >"$WORK_DIR/migrate-plan.json"
  grep -Fq "\"source_schema\":$source_schema" "$WORK_DIR/migrate-plan.json"
  grep -Fq "\"target_schema\":$target_schema" "$WORK_DIR/migrate-plan.json"
  docker run --rm \
    -e OVPN_MAINTENANCE=true \
    -v "$data_dir:/etc/openvpn" \
    "$TARGET_IMAGE" migrate apply --yes >"$WORK_DIR/migrate-apply.out"
  test "$(docker run --rm -v "$data_dir:/etc/openvpn" "$TARGET_IMAGE" ovpn state show)" = HEALTHY
fi

auto_start_log="$WORK_DIR/target-start.log"
set +e
docker run --rm \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e OVPN_ENDPOINT=handoff.example.test \
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

printf 'image handoff smoke passed (network=%s source=%s/schema%s target=%s/schema%s)\n' \
  "$NETWORK" "$SOURCE_IMAGE" "$source_schema" "$TARGET_IMAGE" "$target_schema"
