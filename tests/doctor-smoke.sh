#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"
export OVPN_SERVER_NAME=openvpn-server

make_healthy() {
  local data_dir="$1"

  mkdir -p "$data_dir/config" "$data_dir/meta" "$data_dir/server" "$data_dir/pki/private" "$data_dir/pki/issued" "$data_dir/secrets"
  : >"$data_dir/config/project.env"
  : >"$data_dir/config/schema-version"
  printf '{\n  "ca_fingerprint_sha256": "FAKE:CA:FINGERPRINT"\n}\n' >"$data_dir/meta/instance.json"
  : >"$data_dir/pki/ca.crt"
  : >"$data_dir/pki/private/ca.key"
  : >"$data_dir/pki/issued/$OVPN_SERVER_NAME.crt"
  : >"$data_dir/pki/private/$OVPN_SERVER_NAME.key"
  : >"$data_dir/pki/index.txt"
  : >"$data_dir/pki/serial"
  : >"$data_dir/pki/crl.pem"
  : >"$data_dir/secrets/tls-crypt.key"
  : >"$data_dir/server/server.conf"
}

snapshot() {
  find "$1" -type f -print0 | sort -z | xargs -0 sha256sum
}

healthy="$TMP_DIR/healthy"
make_healthy "$healthy"
before="$(snapshot "$healthy")"
OVPN_DATA_DIR="$healthy" "$OVPN" state doctor --json >"$TMP_DIR/healthy.json" 2>"$TMP_DIR/healthy.err"
after="$(snapshot "$healthy")"
[ "$before" = "$after" ] || {
  echo 'doctor modified a healthy fixture' >&2
  exit 1
}
[ ! -s "$TMP_DIR/healthy.err" ] || {
  echo 'doctor emitted unexpected stderr output for healthy state' >&2
  exit 1
}
grep -Fq '"state": "HEALTHY"' "$TMP_DIR/healthy.json"
grep -Fq '"issues": [' "$TMP_DIR/healthy.json"
OVPN_DATA_DIR="$healthy" "$OVPN" state doctor >"$TMP_DIR/healthy.txt"
grep -Fxq 'State: HEALTHY' "$TMP_DIR/healthy.txt"
grep -Fxq 'Issues: none' "$TMP_DIR/healthy.txt"

pending="$TMP_DIR/pending"
cp -a "$healthy" "$pending"
mkdir -p "$pending/data"
cat >"$pending/data/client-ip.csv" <<'EOF'
# client,ip
draft,
EOF
cat >"$pending/meta/client-ip.applied.csv" <<'EOF'
# client,ip
applied,
EOF
cat >"$pending/meta/client-state.csv" <<'EOF'
# client,state
applied,active
EOF
: >"$pending/meta/audit.jsonl"
printf 'V\t9999\t\t01\tunknown\t/CN=applied\n' >"$pending/pki/index.txt"
: >"$pending/pki/issued/applied.crt"
: >"$pending/pki/private/applied.key"
mkdir -p "$pending/clients/active"
: >"$pending/clients/active/applied.ovpn"
chmod 600 "$pending/data/client-ip.csv" "$pending/meta/client-ip.applied.csv" "$pending/meta/client-state.csv" "$pending/meta/audit.jsonl"
before="$(snapshot "$pending")"
OVPN_DATA_DIR="$pending" "$OVPN" state doctor >"$TMP_DIR/pending.txt" 2>"$TMP_DIR/pending.err"
after="$(snapshot "$pending")"
[ "$before" = "$after" ] || {
  echo 'doctor adopted a pending client-IP draft' >&2
  exit 1
}
[ ! -s "$TMP_DIR/pending.err" ]
grep -Fxq 'State: HEALTHY' "$TMP_DIR/pending.txt"
grep -Fq 'client-IP draft is out of sync with the applied registry; the next write operation will restore it automatically' "$TMP_DIR/pending.txt"

critical="$TMP_DIR/critical"
cp -a "$healthy" "$critical"
rm "$critical/pki/index.txt"
before="$(snapshot "$critical")"
set +e
OVPN_DATA_DIR="$critical" "$OVPN" state doctor --json >"$TMP_DIR/critical.json" 2>"$TMP_DIR/critical.err"
status=$?
set -e
after="$(snapshot "$critical")"
if [ "$status" -ne 78 ]; then
  echo "critical doctor returned $status instead of 78" >&2
  exit 1
fi
[ "$before" = "$after" ] || {
  echo 'doctor modified a critical fixture' >&2
  exit 1
}
[ ! -s "$TMP_DIR/critical.err" ] || {
  echo 'doctor emitted unexpected stderr output for critical state' >&2
  exit 1
}
grep -Fq '"state": "CRITICAL"' "$TMP_DIR/critical.json"
grep -Fq '"id": "PKI_INDEX_MISSING"' "$TMP_DIR/critical.json"
grep -Fq '"severity": "critical"' "$TMP_DIR/critical.json"
grep -Fq '"action": "RESTORE_PKI_DATABASE"' "$TMP_DIR/critical.json"

quoted="$TMP_DIR/quoted"
cp -a "$healthy" "$quoted"
printf 'V\t9999\t\t/CN=bad"client\n' >"$quoted/pki/index.txt"
: >"$quoted/pki/issued/bad\"client.crt"
: >"$quoted/pki/private/bad\"client.key"
OVPN_DATA_DIR="$quoted" "$OVPN" state doctor --json >"$TMP_DIR/quoted.json"
grep -Fq '"id": "CLIENT_PROFILE_MISSING_bad\"client"' "$TMP_DIR/quoted.json"

printf 'doctor smoke passed\n'
