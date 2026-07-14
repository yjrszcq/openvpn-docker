#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_POOL_PERSIST_FILE="$TMP_DIR/leases/pool-persist.txt"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"

"$OVPN" config init
mkdir -p "$OVPN_DATA_DIR/data" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/pki"
printf '%s\n' \
  $'V\t30000101000000Z\t\t01\tunknown\t/CN=alpha' \
  $'V\t30000101000000Z\t\t02\tunknown\t/CN=bravo' \
  $'V\t30000101000000Z\t\t03\tunknown\t/CN=zulu' \
  >"$OVPN_DATA_DIR/pki/index.txt"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
zulu,
alpha,10.88.0.4
bravo,10.88.0.3
EOF
cat >"$OVPN_DATA_DIR/meta/client-ip.applied.csv" <<'EOF'
# client,ip
alpha,10.88.0.4
bravo,10.88.0.3
zulu,
EOF
: >"$OVPN_DATA_DIR/meta/audit.jsonl"
mkdir -p "$(dirname "$OVPN_POOL_PERSIST_FILE")"
cat >"$OVPN_POOL_PERSIST_FILE" <<'EOF'
alpha,10.88.0.200
zulu,10.88.0.201
unrelated,10.88.0.202
EOF

before_validate="$(sha256sum "$OVPN_DATA_DIR/data/client-ip.csv")"
"$OVPN" client-ip validate >"$TMP_DIR/validate.out"
grep -Fqx 'client-ip registry draft is valid' "$TMP_DIR/validate.out"
after_validate="$(sha256sum "$OVPN_DATA_DIR/data/client-ip.csv")"
[ "$before_validate" = "$after_validate" ] || {
  echo 'validate modified the draft' >&2
  exit 1
}

"$OVPN" client-ip apply >"$TMP_DIR/apply.out"
grep -Fqx 'client-ip registry applied' "$TMP_DIR/apply.out"
cat >"$TMP_DIR/expected.csv" <<'EOF'
# client,ip
bravo,10.88.0.3
alpha,10.88.0.4
zulu,
EOF
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
grep -Fq '"outcome":"applied"' "$OVPN_DATA_DIR/meta/audit.jsonl"
grep -Fqx 'ifconfig-push 10.88.0.3 255.255.255.0' "$OVPN_DATA_DIR/ccd/bravo"
grep -Fqx 'ifconfig-push 10.88.0.4 255.255.255.0' "$OVPN_DATA_DIR/ccd/alpha"
test ! -e "$OVPN_DATA_DIR/ccd/zulu"

cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
alpha,10.88.0.3
bravo,10.88.0.3
zulu,
EOF
if "$OVPN" client-ip apply >"$TMP_DIR/rejected.out" 2>"$TMP_DIR/rejected.err"; then
  echo 'duplicate-IP draft unexpectedly applied' >&2
  exit 1
fi
grep -Fq "duplicates static IP '10.88.0.3'" "$TMP_DIR/rejected.err"
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
grep -Fq '"outcome":"rejected"' "$OVPN_DATA_DIR/meta/audit.jsonl"
if grep -Eq 'alpha|10\.88\.0\.3' "$OVPN_DATA_DIR/meta/audit.jsonl"; then
  echo 'audit log contains client or IP details' >&2
  exit 1
fi

cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
zulu,
alpha,10.88.0.4
bravo,10.88.0.3
EOF
"$OVPN" client-ip sync >"$TMP_DIR/sync.out"
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/data/client-ip.csv"

cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
alpha,10.88.0.128
bravo,10.88.0.2
zulu,
EOF
"$OVPN" client-ip apply >"$TMP_DIR/boundary.out"
cat >"$TMP_DIR/boundary.csv" <<'EOF'
# client,ip
bravo,10.88.0.2
alpha,10.88.0.128
zulu,
EOF
cmp "$TMP_DIR/boundary.csv" "$OVPN_DATA_DIR/data/client-ip.csv"

cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
alpha,10.88.0.129
bravo,10.88.0.2
zulu,
EOF
if "$OVPN" client-ip apply >"$TMP_DIR/pool-overlap.out" 2>"$TMP_DIR/pool-overlap.err"; then
  echo 'dynamic-pool IP draft unexpectedly applied' >&2
  exit 1
fi
grep -Fq 'outside the static address region' "$TMP_DIR/pool-overlap.err"
cmp "$TMP_DIR/boundary.csv" "$OVPN_DATA_DIR/data/client-ip.csv"

cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
alpha,
bravo,10.88.0.2
zulu,
EOF
"$OVPN" client-ip apply >"$TMP_DIR/dynamic.out"
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
grep -Fqx 'zulu,10.88.0.201' "$OVPN_POOL_PERSIST_FILE"
grep -Fqx 'unrelated,10.88.0.202' "$OVPN_POOL_PERSIST_FILE"
if grep -Fq 'alpha,10.88.0.200' "$OVPN_POOL_PERSIST_FILE"; then
  echo 'dynamic lease was not cleared for a converted client' >&2
  exit 1
fi
cp "$OVPN_DATA_DIR/meta/client-ip.applied.csv" "$TMP_DIR/before-failure.csv"
ccd_before="$(sha256sum "$OVPN_DATA_DIR/ccd/bravo")"
lease_before="$(sha256sum "$OVPN_POOL_PERSIST_FILE")"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# client,ip
alpha,10.88.0.128
bravo,
zulu,
EOF
if OVPN_CLIENT_IP_APPLY_FAIL_AFTER=ccd "$OVPN" client-ip apply >"$TMP_DIR/derived-failure.out" 2>"$TMP_DIR/derived-failure.err"; then
  echo 'injected derived-state failure unexpectedly applied' >&2
  exit 1
fi
grep -Fq 'injected client-ip apply failure after ccd' "$TMP_DIR/derived-failure.err"
cmp "$TMP_DIR/before-failure.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$TMP_DIR/before-failure.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
[ "$ccd_before" = "$(sha256sum "$OVPN_DATA_DIR/ccd/bravo")" ]
[ "$lease_before" = "$(sha256sum "$OVPN_POOL_PERSIST_FILE")" ]
grep -Fqx 'ifconfig-push 10.88.0.2 255.255.255.0' "$OVPN_DATA_DIR/ccd/bravo"

printf 'client-ip apply smoke passed\n'
