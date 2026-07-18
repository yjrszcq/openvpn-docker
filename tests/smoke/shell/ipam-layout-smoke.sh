#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"

"$OVPN" config apply
"$OVPN" config show >"$TMP_DIR/default.out"
grep -Fqx 'OVPN_CONFIG_VERSION=3' "$TMP_DIR/default.out"
grep -Fqx 'OVPN_TOPOLOGY=subnet' "$TMP_DIR/default.out"
grep -Fqx 'OVPN_DYNAMIC_POOL_SIZE=126' "$TMP_DIR/default.out"

OVPN_DATA_DIR="$TMP_DIR/static" OVPN_DYNAMIC_POOL_SIZE=0 "$OVPN" config apply
OVPN_DATA_DIR="$TMP_DIR/static" "$OVPN" config show >"$TMP_DIR/static.out"
grep -Fqx 'OVPN_DYNAMIC_POOL_SIZE=0' "$TMP_DIR/static.out"

OVPN_DATA_DIR="$TMP_DIR/dynamic" OVPN_DYNAMIC_POOL_SIZE=253 "$OVPN" config apply
OVPN_DATA_DIR="$TMP_DIR/dynamic" "$OVPN" config show >"$TMP_DIR/dynamic.out"
grep -Fqx 'OVPN_DYNAMIC_POOL_SIZE=253' "$TMP_DIR/dynamic.out"

if OVPN_DATA_DIR="$TMP_DIR/invalid-pool" OVPN_DYNAMIC_POOL_SIZE=254 "$OVPN" config apply >"$TMP_DIR/invalid-pool.out" 2>"$TMP_DIR/invalid-pool.err"; then
  echo 'out-of-range dynamic pool unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'OVPN_DYNAMIC_POOL_SIZE must be between 0 and 253' "$TMP_DIR/invalid-pool.err"

if OVPN_DATA_DIR="$TMP_DIR/invalid-network" OVPN_NETWORK=10.88.0.1/24 "$OVPN" config apply >"$TMP_DIR/invalid-network.out" 2>"$TMP_DIR/invalid-network.err"; then
  echo 'non-canonical network unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'OVPN_NETWORK must be a canonical network CIDR' "$TMP_DIR/invalid-network.err"

if OVPN_DATA_DIR="$TMP_DIR/small-network" OVPN_NETWORK=10.88.0.0/31 "$OVPN" config apply >"$TMP_DIR/small-network.out" 2>"$TMP_DIR/small-network.err"; then
  echo 'undersized network unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'OVPN_NETWORK must provide at least one client address' "$TMP_DIR/small-network.err"

printf 'ipam layout smoke passed\n'
