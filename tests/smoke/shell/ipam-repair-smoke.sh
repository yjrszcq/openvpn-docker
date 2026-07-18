#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/runtime"
export OVPN_LEASE_DIR="$TMP_DIR/runtime/leases"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_SERVER_NAME=openvpn-server

"$OVPN" config apply
mkdir -p \
  "$OVPN_DATA_DIR/meta" \
  "$OVPN_DATA_DIR/data" \
  "$OVPN_DATA_DIR/ccd" \
  "$OVPN_DATA_DIR/clients/active" \
  "$OVPN_DATA_DIR/pki/private" \
  "$OVPN_DATA_DIR/pki/issued" \
  "$OVPN_DATA_DIR/secrets" \
  "$OVPN_DATA_DIR/server"
printf '{\n  "ca_fingerprint_sha256": "FAKE:CA:FINGERPRINT"\n}\n' >"$OVPN_DATA_DIR/meta/instance.json"
: >"$OVPN_DATA_DIR/pki/ca.crt"
: >"$OVPN_DATA_DIR/pki/private/ca.key"
: >"$OVPN_DATA_DIR/pki/issued/openvpn-server.crt"
: >"$OVPN_DATA_DIR/pki/private/openvpn-server.key"
: >"$OVPN_DATA_DIR/pki/serial"
: >"$OVPN_DATA_DIR/pki/crl.pem"
: >"$OVPN_DATA_DIR/secrets/tls-crypt.key"
: >"$OVPN_DATA_DIR/server/server.conf"
printf '%s\n' \
  $'V\t30000101000000Z\t\t01\tunknown\t/CN=dynamic' \
  $'V\t30000101000000Z\t\t02\tunknown\t/CN=static' \
  >"$OVPN_DATA_DIR/pki/index.txt"
: >"$OVPN_DATA_DIR/pki/issued/dynamic.crt"
: >"$OVPN_DATA_DIR/pki/private/dynamic.key"
: >"$OVPN_DATA_DIR/pki/issued/static.crt"
: >"$OVPN_DATA_DIR/pki/private/static.key"
: >"$OVPN_DATA_DIR/clients/active/dynamic.ovpn"
: >"$OVPN_DATA_DIR/clients/active/static.ovpn"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# id,name,ip
11111111-1111-4111-8111-111111111111,dynamic,
22222222-2222-4222-8222-222222222222,static,10.88.0.2
EOF
cp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
cat >"$OVPN_DATA_DIR/meta/client-state.csv" <<'EOF'
# id,name,state
11111111-1111-4111-8111-111111111111,dynamic,active
22222222-2222-4222-8222-222222222222,static,active
EOF
: >"$OVPN_DATA_DIR/meta/audit.jsonl"
printf 'ifconfig-push 10.88.0.3 255.255.255.0\n' >"$OVPN_DATA_DIR/ccd/static"
chmod 600 \
  "$OVPN_DATA_DIR/data/client-ip.csv" \
  "$OVPN_DATA_DIR/meta/client-ip.applied.csv" \
  "$OVPN_DATA_DIR/meta/client-state.csv" \
  "$OVPN_DATA_DIR/meta/audit.jsonl" \
  "$OVPN_DATA_DIR/ccd/static"

"$OVPN" repair plan >"$TMP_DIR/plan.out"
grep -Fq '[SAFE] SYNCHRONIZE_CLIENT_IP_CCD' "$TMP_DIR/plan.out"
grep -Fq '[SAFE] NORMALIZE_CLIENT_IP_DRAFT' "$TMP_DIR/plan.out"
grep -Fq '[SAFE] NORMALIZE_CLIENT_IP_APPLIED' "$TMP_DIR/plan.out"
"$OVPN" repair apply >"$TMP_DIR/repair.out" 2>"$TMP_DIR/repair.err"
cat >"$TMP_DIR/expected.csv" <<'EOF'
# id,name,ip
22222222-2222-4222-8222-222222222222,static,10.88.0.2
11111111-1111-4111-8111-111111111111,dynamic,
EOF
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
grep -Fqx 'ifconfig-push 10.88.0.2 255.255.255.0' "$OVPN_DATA_DIR/ccd/static"
test ! -e "$OVPN_DATA_DIR/ccd/dynamic"
[ "$("$OVPN" state show)" = HEALTHY ]

ccd_before="$(sha256sum "$OVPN_DATA_DIR/ccd/static")"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# id,name,ip
22222222-2222-4222-8222-222222222222,static,10.88.0.129
11111111-1111-4111-8111-111111111111,dynamic,
EOF
cp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
if "$OVPN" repair apply >"$TMP_DIR/invalid.out" 2>"$TMP_DIR/invalid.err"; then
  echo 'repair unexpectedly accepted an invalid static IP registry' >&2
  exit 1
fi
grep -Fq 'CLIENT_IP_APPLIED_INVALID' "$TMP_DIR/invalid.err"
[ "$ccd_before" = "$(sha256sum "$OVPN_DATA_DIR/ccd/static")" ] || {
  echo 'repair changed derived state for an invalid registry' >&2
  exit 1
}

cp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/data/client-ip.csv"
cp "$TMP_DIR/expected.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
printf '{"unexpected":"audit payload"}\n' >"$OVPN_DATA_DIR/meta/audit.jsonl"
if "$OVPN" state doctor --json >"$TMP_DIR/audit.json" 2>"$TMP_DIR/audit.err"; then
  echo 'doctor unexpectedly accepted malformed audit state' >&2
  exit 1
fi
grep -Fq '"id": "CLIENT_IP_AUDIT_INVALID"' "$TMP_DIR/audit.json"
printf 'IPAM repair smoke passed\n'
