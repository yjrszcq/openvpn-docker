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

printf 'client-ip apply smoke passed\n'
