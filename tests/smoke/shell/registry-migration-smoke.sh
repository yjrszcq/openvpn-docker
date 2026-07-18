#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d)"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

export OVPN_DATA_DIR="$TMP_DIR/v1"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"

# shellcheck source=/dev/null
. "$LIB_DIR/common.sh"
. "$LIB_DIR/ipam.sh"
. "$LIB_DIR/config.sh"
. "$LIB_DIR/registry.sh"

if declare -F ovpn_migration_1_to_2_apply_staged >/dev/null; then
  echo 'historical migration was loaded by current runtime modules' >&2
  exit 1
fi

mkdir -p "$OVPN_DATA_DIR/config" "$OVPN_DATA_DIR/pki"
cat >"$OVPN_DATA_DIR/config/project.env" <<'EOF'
OVPN_CONFIG_VERSION=1
OVPN_ENDPOINT=vpn.example.test
OVPN_PROTO=udp
OVPN_PORT=1194
OVPN_NETWORK=10.88.0.0/24
OVPN_NAT=true
OVPN_NAT_INTERFACE=auto
OVPN_REDIRECT_GATEWAY=false
OVPN_CLIENT_TO_CLIENT=false
OVPN_DNS=
OVPN_ROUTES=
EOF
printf '%s\n' \
  $'V\t30000101000000Z\t\t01\tunknown\t/CN=laptop' \
  $'R\t30000101000000Z\t260101000000Z\t02\tunknown\t/CN=phone' \
  $'V\t30000101000000Z\t\t03\tunknown\t/CN=openvpn-server' \
  >"$OVPN_DATA_DIR/pki/index.txt"
printf '1\n' >"$OVPN_DATA_DIR/config/schema-version"

OVPN_LIB_DIR="$LIB_DIR" OVPN_MAINTENANCE=true "$OVPN" migrate plan --json >"$TMP_DIR/plan.json"
grep -Fq '"chain":"1-to-2"' "$TMP_DIR/plan.json"
grep -Fq '"clients":2' "$TMP_DIR/plan.json"

# shellcheck source=/dev/null
. "$LIB_DIR/migrations/1-to-2.sh"
ovpn_migration_1_to_2_apply_staged

grep -Fqx 'OVPN_CONFIG_VERSION=2' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_TOPOLOGY=subnet' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_DYNAMIC_POOL_SIZE=126' "$OVPN_DATA_DIR/config/project.env"
cmp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
grep -Fqx '# client,ip' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'laptop,' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'phone,' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'laptop,active' "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx 'phone,revoked' "$OVPN_DATA_DIR/meta/client-state.csv"
test ! -s "$OVPN_DATA_DIR/meta/audit.jsonl"

if (
  export OVPN_DATA_DIR="$TMP_DIR/incomplete-v2"
  export OVPN_ENDPOINT="vpn.example.test"
  export OVPN_NETWORK="10.88.0.0/24"
  # shellcheck source=/dev/null
  . "$LIB_DIR/common.sh"
  . "$LIB_DIR/ipam.sh"
  . "$LIB_DIR/config.sh"
  . "$LIB_DIR/registry.sh"
  mkdir -p "$OVPN_DATA_DIR/config"
  cat >"$OVPN_DATA_DIR/config/project.env" <<'EOF'
OVPN_CONFIG_VERSION=2
OVPN_ENDPOINT=vpn.example.test
OVPN_PROTO=udp
OVPN_PORT=1194
OVPN_NETWORK=10.88.0.0/24
OVPN_TOPOLOGY=subnet
OVPN_DYNAMIC_POOL_SIZE=126
OVPN_NAT=true
OVPN_NAT_INTERFACE=auto
OVPN_REDIRECT_GATEWAY=false
OVPN_CLIENT_TO_CLIENT=false
OVPN_DNS=
OVPN_ROUTES=
EOF
  # shellcheck source=/dev/null
  . "$LIB_DIR/migrations/1-to-2.sh"
  ovpn_migration_1_to_2_apply_staged
) >"$TMP_DIR/incomplete-v2.out" 2>"$TMP_DIR/incomplete-v2.err"; then
  echo 'incomplete V2 registry unexpectedly migrated' >&2
  exit 1
fi
grep -Fq 'expected schema 1 configuration' "$TMP_DIR/incomplete-v2.err"

printf 'registry migration smoke passed\n'
