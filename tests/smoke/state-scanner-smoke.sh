#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_DATA_DIR="$TMP_DIR/missing"

if [ "$("$OVPN" state show)" != EMPTY ]; then
  echo 'missing data directory should be EMPTY' >&2
  exit 1
fi
if [ -e "$OVPN_DATA_DIR" ]; then
  echo 'state scan unexpectedly created the data directory' >&2
  exit 1
fi

export OVPN_DATA_DIR="$TMP_DIR/ignored"
mkdir -p "$OVPN_DATA_DIR"
touch "$OVPN_DATA_DIR/lost+found" "$OVPN_DATA_DIR/.DS_Store"
if [ "$("$OVPN" state show)" != EMPTY ]; then
  echo 'whitelisted data directory entries should remain EMPTY' >&2
  exit 1
fi

for artifact in config pki meta clients server.conf unknown.key .staging-init-test; do
  export OVPN_DATA_DIR="$TMP_DIR/$artifact"
  case "$artifact" in
    config|pki|meta|clients|.staging-init-test)
      mkdir -p "$OVPN_DATA_DIR/$artifact"
      ;;
    *)
      mkdir -p "$OVPN_DATA_DIR"
      : >"$OVPN_DATA_DIR/$artifact"
      ;;
  esac
  if [ "$("$OVPN" state show)" = EMPTY ]; then
    echo "artifact $artifact was incorrectly classified as EMPTY" >&2
    exit 1
  fi
done

printf 'state scanner smoke passed\n'
