#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_LEASE_DIR="$TMP_DIR/leases"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"

"$OVPN" config apply
mkdir -p "$OVPN_DATA_DIR/data" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/pki"
printf '%s\n' \
  $'V\t30000101000000Z\t\t01\tunknown\t/CN=alpha' \
  $'V\t30000101000000Z\t\t02\tunknown\t/CN=bravo' \
  $'V\t30000101000000Z\t\t03\tunknown\t/CN=zulu' \
  >"$OVPN_DATA_DIR/pki/index.txt"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
bravo,10.88.0.3
alpha,10.88.0.4
zulu,
EOF
cp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
printf '%s\n' '# client,state' 'alpha,active' 'bravo,active' 'zulu,active' >"$OVPN_DATA_DIR/meta/client-state.csv"
: >"$OVPN_DATA_DIR/meta/audit.jsonl"
mkdir -p "$OVPN_LEASE_DIR"
printf '10.88.0.200\n' >"$OVPN_LEASE_DIR/alpha"
printf '10.88.0.201\n' >"$OVPN_LEASE_DIR/zulu"
printf '10.88.0.202\n' >"$OVPN_LEASE_DIR/unrelated"

canonical="$(mktemp "$TMP_DIR/canonical.XXXXXX")"
cat >"$canonical" <<'EOF'
# client,ip
bravo,10.88.0.3
alpha,10.88.0.4
zulu,
EOF

# Test: set triggers apply transaction, canonical ordering, CCD generation
"$OVPN" client ip set bravo --ip 10.88.0.3 >"$TMP_DIR/apply.out" 2>&1
grep -Fq 'set client' "$TMP_DIR/apply.out"
cmp "$canonical" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$canonical" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
grep -Fq '"outcome":"applied"' "$OVPN_DATA_DIR/meta/audit.jsonl"
grep -Fqx 'ifconfig-push 10.88.0.3 255.255.255.0' "$OVPN_DATA_DIR/ccd/bravo"
grep -Fqx 'ifconfig-push 10.88.0.4 255.255.255.0' "$OVPN_DATA_DIR/ccd/alpha"
test ! -e "$OVPN_DATA_DIR/ccd/zulu"

# Test: duplicate IP rejected, state rolled back
if "$OVPN" client ip set bravo --ip 10.88.0.4 >"$TMP_DIR/rejected.out" 2>&1; then
  echo 'conflicting-IP set unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq "static IP '10.88.0.4' is already assigned" "$TMP_DIR/rejected.out"
cmp "$canonical" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$canonical" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"

# Test: set with same value re-applies cleanly
"$OVPN" client ip set bravo --ip 10.88.0.3 >"$TMP_DIR/sync.out" 2>&1
cmp "$canonical" "$OVPN_DATA_DIR/data/client-ip.csv"

# Test: boundary addresses in static region
cat >"$TMP_DIR/boundary.csv" <<'EOF'
# client,ip
bravo,10.88.0.2
alpha,10.88.0.128
zulu,
EOF
"$OVPN" client ip set bravo --ip 10.88.0.2 >"$TMP_DIR/boundary-bravo.out" 2>&1
"$OVPN" client ip set alpha --ip 10.88.0.128 >"$TMP_DIR/boundary-alpha.out" 2>&1
cmp "$TMP_DIR/boundary.csv" "$OVPN_DATA_DIR/data/client-ip.csv"

# Test: address in dynamic pool rejected
cat >"$TMP_DIR/pre-overlap.csv" <<'EOF'
# client,ip
bravo,10.88.0.2
alpha,10.88.0.128
zulu,
EOF
if "$OVPN" client ip set alpha --ip 10.88.0.129 >"$TMP_DIR/pool-overlap.out" 2>&1; then
  echo 'dynamic-pool IP set unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'outside the static address region' "$TMP_DIR/pool-overlap.out"
cmp "$TMP_DIR/pre-overlap.csv" "$OVPN_DATA_DIR/data/client-ip.csv"

# Test: dynamic assignment removes CCD
"$OVPN" client ip set alpha --dynamic >"$TMP_DIR/dynamic.out" 2>&1
cat >"$TMP_DIR/dynamic.csv" <<'EOF'
# client,ip
bravo,10.88.0.2
alpha,
zulu,
EOF
cmp "$TMP_DIR/dynamic.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'ifconfig-push 10.88.0.2 255.255.255.0' "$OVPN_DATA_DIR/ccd/bravo"
test ! -e "$OVPN_DATA_DIR/ccd/alpha"
test ! -e "$OVPN_DATA_DIR/ccd/zulu"
grep -Fqx '10.88.0.201' "$OVPN_LEASE_DIR/zulu"
grep -Fqx '10.88.0.202' "$OVPN_LEASE_DIR/unrelated"
if [ -f "$OVPN_LEASE_DIR/alpha" ]; then
  echo 'dynamic lease was not cleared for a converted client' >&2
  exit 1
fi

# Test: transaction rollback on derived-state failure
cp "$OVPN_DATA_DIR/meta/client-ip.applied.csv" "$TMP_DIR/before-failure.csv"
ccd_before="$(sha256sum "$OVPN_DATA_DIR/ccd/bravo")"
lease_before="$(find "$OVPN_LEASE_DIR" -type f -exec sha256sum {} + | sort -k2 | sha256sum)"
if OVPN_CLIENT_IP_APPLY_FAIL_AFTER=ccd "$OVPN" client ip set zulu --ip 10.88.0.4 >"$TMP_DIR/derived-failure.out" 2>&1; then
  echo 'injected derived-state failure unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'injected client-ip apply failure after ccd' "$TMP_DIR/derived-failure.out"
cmp "$TMP_DIR/before-failure.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$TMP_DIR/before-failure.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
[ "$ccd_before" = "$(sha256sum "$OVPN_DATA_DIR/ccd/bravo")" ]
[ "$lease_before" = "$(find "$OVPN_LEASE_DIR" -type f -exec sha256sum {} + | sort -k2 | sha256sum)" ]
grep -Fqx 'ifconfig-push 10.88.0.2 255.255.255.0' "$OVPN_DATA_DIR/ccd/bravo"

printf 'client-ip set smoke passed\n'
