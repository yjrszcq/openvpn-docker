#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_BACKUP_IMAGE:-szcq/openvpn-server:backup-smoke}"
REQUIRED="${OVPN_BACKUP_REQUIRED:-0}"
SKIP_BUILD="${OVPN_BACKUP_SKIP_BUILD:-0}"
WORK_DIR=''

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'backup/restore smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'backup/restore smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rmdir "$WORK_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'

if [ "$SKIP_BUILD" != 1 ]; then
  OVPN_BUILD_NETWORK=host "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
fi

WORK_DIR="$(mktemp -d)"
mkdir -p "$WORK_DIR/source/data" "$WORK_DIR/source/config" "$WORK_DIR/restored"
chmod 750 "$WORK_DIR/source/data" "$WORK_DIR/source/config" "$WORK_DIR/restored"

cat >"$WORK_DIR/source/config/config.yaml" <<'YAML'
version: 1
server:
  endpoint: vpn.example.com
ipv4:
  network: 10.73.0.0/24
  dynamicPoolSize: 32
YAML

run_control() {
  local data_dir="$1"
  local config_dir="$2"
  shift 2
  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/ovpn-conf" \
    --entrypoint ovpn \
    "$IMAGE" "$@"
}

run_control "$WORK_DIR/source/data" "$WORK_DIR/source/config" server init >/dev/null
run_control "$WORK_DIR/source/data" "$WORK_DIR/source/config" \
  client create laptop --ipv4 10.73.0.20 >/dev/null
run_control "$WORK_DIR/source/data" "$WORK_DIR/source/config" \
  client export --name laptop --output - >"$WORK_DIR/source-laptop.ovpn"
run_control "$WORK_DIR/source/data" "$WORK_DIR/source/config" state doctor --json | grep -Fq '"state":"HEALTHY"'

docker run --rm \
  -v "$WORK_DIR/source/data:/etc/openvpn:ro" \
  --entrypoint sh "$IMAGE" -ec 'test "$(stat -c %a /etc/openvpn/meta/state.db)" = 600'

docker run --rm \
  -v "$WORK_DIR:/work" \
  --entrypoint tar "$IMAGE" \
  --numeric-owner -C /work/source -czf /work/openvpn-v4-backup.tar.gz data config
docker run --rm -v "$WORK_DIR:/work" --entrypoint chmod "$IMAGE" 600 /work/openvpn-v4-backup.tar.gz

run_control "$WORK_DIR/source/data" "$WORK_DIR/source/config" \
  client create after-backup --ipv4 dynamic >/dev/null

docker run --rm \
  -v "$WORK_DIR:/work" \
  --entrypoint tar "$IMAGE" \
  --numeric-owner -C /work/restored -xzf /work/openvpn-v4-backup.tar.gz

run_control "$WORK_DIR/restored/data" "$WORK_DIR/restored/config" state doctor --json | grep -Fq '"state":"HEALTHY"'
clients="$(run_control "$WORK_DIR/restored/data" "$WORK_DIR/restored/config" client list --json)"
grep -Fq '"name":"laptop"' <<<"$clients"
if grep -Fq '"name":"after-backup"' <<<"$clients"; then
  echo 'post-backup mutation appeared in restored state' >&2
  exit 1
fi
run_control "$WORK_DIR/restored/data" "$WORK_DIR/restored/config" \
  client export --name laptop --output - >"$WORK_DIR/restored-laptop.ovpn"
cmp "$WORK_DIR/source-laptop.ovpn" "$WORK_DIR/restored-laptop.ovpn"

printf 'schema 4 backup/restore smoke passed\n'
