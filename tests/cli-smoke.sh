#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_BUILD_INFO="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/build-info.json"

"$OVPN" help >/tmp/ovpn-help.out
if ! grep -q 'Usage: ovpn' /tmp/ovpn-help.out; then
  echo 'help output missing usage' >&2
  exit 1
fi

"$OVPN" version >/tmp/ovpn-version.out
if ! grep -q '"image_version": "0.1.0-dev"' /tmp/ovpn-version.out; then
  echo 'version output missing image_version' >&2
  exit 1
fi

set +e
"$OVPN" init >/tmp/ovpn-init.out 2>/tmp/ovpn-init.err
status=$?
set -e
if [ "$status" -ne 2 ]; then
  echo "init returned $status, expected 2 for phase stub" >&2
  exit 1
fi
if ! grep -q "not implemented" /tmp/ovpn-init.err; then
  echo 'init stub did not explain not implemented state' >&2
  exit 1
fi

set +e
"$OVPN" does-not-exist >/tmp/ovpn-unknown.out 2>/tmp/ovpn-unknown.err
status=$?
set -e
if [ "$status" -ne 64 ]; then
  echo "unknown command returned $status, expected 64" >&2
  exit 1
fi

printf 'cli smoke passed\n'
