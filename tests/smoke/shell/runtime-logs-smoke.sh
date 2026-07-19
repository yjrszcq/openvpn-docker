#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
python3 "$ROOT_DIR/tests/smoke/python/runtime-logs-smoke.py"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_DATA_DIR="$TMP_DIR/data"
export OVPN_ENDPOINT=vpn.example.test
client_id=11111111-1111-4111-8111-111111111111
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" config apply
mkdir -p "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/logs"
printf '%s\n' '# id,name,state' "$client_id,laptop,active" \
  >"$OVPN_DATA_DIR/meta/client-state.csv"
printf '>LOG:1,N,connected %s\n' "$client_id" >"$OVPN_DATA_DIR/logs/openvpn.log"
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime logs -l 1 >"$TMP_DIR/logs.out"
grep -Fqx ">LOG:1,N,connected laptop [111111111111]" "$TMP_DIR/logs.out"
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime logs -l 1 -t >"$TMP_DIR/logs-full.out"
grep -Fqx ">LOG:1,N,connected laptop [$client_id]" "$TMP_DIR/logs-full.out"
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime logs --help |
  grep -Fq 'usage: ovpn runtime logs'
