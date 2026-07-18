#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"
export OVPN_SERVER_NAME=openvpn-server
export OVPN_RUNTIME_DIR="$TMP_DIR/runtime"

make_healthy() {
  local data_dir="$1"

  mkdir -p "$data_dir/config" "$data_dir/data" "$data_dir/meta" "$data_dir/server" "$data_dir/pki/private" "$data_dir/pki/issued" "$data_dir/secrets"
  printf 'OVPN_CONFIG_VERSION=3\n' >"$data_dir/config/project.env"
  printf '3\n' >"$data_dir/config/schema-version"
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
  printf '# id,name,ip\n' >"$data_dir/data/client-ip.csv"
  cp "$data_dir/data/client-ip.csv" "$data_dir/meta/client-ip.applied.csv"
  printf '# id,name,state\n' >"$data_dir/meta/client-state.csv"
  : >"$data_dir/meta/audit.jsonl"
  chmod 600 "$data_dir/data/client-ip.csv" "$data_dir/meta/client-ip.applied.csv" "$data_dir/meta/client-state.csv" "$data_dir/meta/audit.jsonl"
}

snapshot() {
  find "$1" -type f ! -name .ovpn-data.lock -print0 | sort -z | xargs -0 sha256sum
}

repairable="$TMP_DIR/repairable"
make_healthy "$repairable"
rm "$repairable/config/schema-version" "$repairable/meta/instance.json" "$repairable/server/server.conf" "$repairable/pki/crl.pem"
before="$(snapshot "$repairable")"
OVPN_DATA_DIR="$repairable" "$OVPN" repair plan --json >"$TMP_DIR/repairable.json" 2>"$TMP_DIR/repairable.err"
after="$(snapshot "$repairable")"
[ "$before" = "$after" ] || {
  echo 'repair plan modified a repairable fixture' >&2
  exit 1
}
[ ! -e "$OVPN_RUNTIME_DIR" ] || {
  echo 'repair plan created the runtime directory' >&2
  exit 1
}
[ ! -s "$TMP_DIR/repairable.err" ] || {
  echo 'repair plan emitted unexpected stderr output' >&2
  exit 1
}
grep -Fq '"state": "DEGRADED_REPAIRABLE"' "$TMP_DIR/repairable.json"
grep -Fq '"id": "WRITE_SCHEMA_VERSION"' "$TMP_DIR/repairable.json"
grep -Fq '"id": "REBUILD_METADATA"' "$TMP_DIR/repairable.json"
grep -Fq '"id": "RENDER_SERVER_CONFIG"' "$TMP_DIR/repairable.json"
grep -Fq '"id": "REGENERATE_CRL"' "$TMP_DIR/repairable.json"
grep -Fq '"id": "ENSURE_RUNTIME_DIRECTORY"' "$TMP_DIR/repairable.json"

critical="$TMP_DIR/critical"
cp -a "$repairable" "$critical"
printf '3\n' >"$critical/config/schema-version"
: >"$critical/meta/instance.json"
: >"$critical/server/server.conf"
: >"$critical/pki/crl.pem"
rm "$critical/pki/index.txt"
before="$(snapshot "$critical")"
recoverable="$TMP_DIR/recoverable"
cp -a "$repairable" "$recoverable"
printf '3\n' >"$recoverable/config/schema-version"
printf '{\n  "ca_fingerprint_sha256": "FAKE:CA:FINGERPRINT"\n}\n' >"$recoverable/meta/instance.json"
: >"$recoverable/server/server.conf"
: >"$recoverable/pki/crl.pem"
mkdir -p "$recoverable/clients/active"
printf '%s\n' '<ca>' 'FAKE CA CERT' '</ca>' >"$recoverable/clients/active/laptop.ovpn"
rm "$recoverable/pki/ca.crt"
OVPN_DATA_DIR="$recoverable" "$OVPN" repair plan --json >"$TMP_DIR/recoverable.json"
grep -Fq '"id": "RECOVER_CA_CERT"' "$TMP_DIR/recoverable.json"
grep -Fq '"kind": "recover"' "$TMP_DIR/recoverable.json"

reissuable="$TMP_DIR/reissuable"
cp -a "$recoverable" "$reissuable"
: >"$reissuable/pki/ca.crt"
rm "$reissuable/pki/issued/$OVPN_SERVER_NAME.crt"
OVPN_DATA_DIR="$reissuable" "$OVPN" repair plan --json >"$TMP_DIR/reissuable.json"
grep -Fq '"id": "SERVER_CERT_MISSING"' "$TMP_DIR/reissuable.json"
grep -Fq '"severity": "reissuable"' "$TMP_DIR/reissuable.json"

set +e
OVPN_DATA_DIR="$critical" "$OVPN" repair plan >"$TMP_DIR/critical.txt" 2>"$TMP_DIR/critical.err"
exit_code=$?
set -e
after="$(snapshot "$critical")"
if [ "$exit_code" -ne 78 ]; then
  echo "critical repair plan returned $exit_code instead of 78" >&2
  exit 1
fi
[ "$before" = "$after" ] || {
  echo 'repair plan modified a critical fixture' >&2
  exit 1
}
grep -Fq '[BLOCKED] PKI_INDEX_MISSING' "$TMP_DIR/critical.txt"
set +e
OVPN_DATA_DIR="$critical" "$OVPN" repair apply >"$TMP_DIR/critical-repair.out" 2>"$TMP_DIR/critical-repair.err"
exit_code=$?
set -e
if [ "$exit_code" -ne 78 ]; then
  echo "critical repair returned $exit_code instead of 78" >&2
  exit 1
fi
[ "$before" = "$(snapshot "$critical")" ] || {
  echo 'critical repair modified a fixture' >&2
  exit 1
}

set +e
OVPN_DATA_DIR="$repairable" "$OVPN" repair apply --unsupported >"$TMP_DIR/usage.out" 2>"$TMP_DIR/usage.err"
exit_code=$?
set -e
if [ "$exit_code" -ne 64 ]; then
  echo "repair with an unsupported argument returned $exit_code instead of 64" >&2
  exit 1
fi
grep -Fq 'usage: ovpn repair apply' "$TMP_DIR/usage.err"

printf 'repair plan smoke passed\n'
