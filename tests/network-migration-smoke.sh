#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"
trap 'rm -rf "$TMP_DIR"' EXIT

cat >"$FAKE_BIN/openvpn" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = --version ]; then
  printf 'OpenVPN 2.7.5 test-build\n'
fi
EOF
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_POOL_PERSIST_FILE="$TMP_DIR/leases/pool-persist.txt"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"

"$OVPN" config init
mkdir -p "$OVPN_DATA_DIR/data" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/pki" "$(dirname "$OVPN_POOL_PERSIST_FILE")"
printf '%s\n' \
  $'V\t30000101000000Z\t\t01\tunknown\t/CN=alpha' \
  $'V\t30000101000000Z\t\t02\tunknown\t/CN=bravo' \
  >"$OVPN_DATA_DIR/pki/index.txt"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
alpha,10.88.0.20
bravo,
EOF
cp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
printf '%s\n' '# client,state' 'alpha,active' 'bravo,active' >"$OVPN_DATA_DIR/meta/client-state.csv"
: >"$OVPN_DATA_DIR/meta/audit.jsonl"
printf '%s\n' 'bravo,10.88.0.200' >"$OVPN_POOL_PERSIST_FILE"
"$OVPN" client-ip apply

"$OVPN" network reconfigure --network 10.89.0.0/24 --dynamic-pool-size 100 --dry-run >"$TMP_DIR/dry.out"
grep -Fq 'Network: 10.88.0.0/24 -> 10.89.0.0/24' "$TMP_DIR/dry.out"
grep -Fqx 'OVPN_NETWORK=10.88.0.0/24' "$OVPN_DATA_DIR/config/project.env"

"$OVPN" network reconfigure --network 10.89.0.0/24 --dynamic-pool-size 100 --yes >"$TMP_DIR/apply.out"
grep -Fqx 'OVPN_NETWORK=10.89.0.0/24' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_DYNAMIC_POOL_SIZE=100' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'alpha,10.89.0.20' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'ifconfig-push 10.89.0.20 255.255.255.0' "$OVPN_DATA_DIR/ccd/alpha"
test ! -s "$OVPN_POOL_PERSIST_FILE"
grep -q '^server 10.89.0.0 255.255.255.0 nopool$' "$OVPN_DATA_DIR/server/server.conf"

if OVPN_NETWORK_MIGRATION_FAIL_HEALTH=true "$OVPN" network reconfigure --network 10.90.0.0/24 --yes >"$TMP_DIR/fail.out" 2>"$TMP_DIR/fail.err"; then
  echo 'failed network migration unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'rollback completed' "$TMP_DIR/fail.err"
grep -Fqx 'OVPN_NETWORK=10.89.0.0/24' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'alpha,10.89.0.20' "$OVPN_DATA_DIR/data/client-ip.csv"

printf 'network migration smoke passed\n'
