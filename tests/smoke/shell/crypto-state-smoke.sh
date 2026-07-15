#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

unset OVPN_OPENSSL_BIN
export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_SERVER_NAME=openvpn-server
# shellcheck source=../rootfs/usr/local/lib/openvpn-container/common.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/common.sh"
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/state.sh"

make_crypto_healthy() {
  local data_dir="$1"
  local fingerprint

  mkdir -p "$data_dir/config" "$data_dir/meta" "$data_dir/server" "$data_dir/pki/private" "$data_dir/pki/issued" "$data_dir/secrets" "$data_dir/ca-db/newcerts"
  printf 'OVPN_CONFIG_VERSION=1\nOVPN_ENDPOINT=vpn.example.test\nOVPN_PROTO=udp\nOVPN_PORT=1194\nOVPN_NETWORK=10.88.0.0/24\nOVPN_NAT=true\nOVPN_NAT_INTERFACE=auto\nOVPN_REDIRECT_GATEWAY=false\nOVPN_CLIENT_TO_CLIENT=false\nOVPN_DNS=\nOVPN_ROUTES=\n' >"$data_dir/config/project.env"
  printf '1\n' >"$data_dir/config/schema-version"
  : >"$data_dir/pki/index.txt"
  printf '01\n' >"$data_dir/pki/serial"
  printf '1000\n' >"$data_dir/ca-db/serial"
  printf '1000\n' >"$data_dir/ca-db/crlnumber"
  : >"$data_dir/ca-db/index.txt"
  : >"$data_dir/secrets/tls-crypt.key"
  : >"$data_dir/server/server.conf"

  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=OpenVPN Container CA' -keyout "$data_dir/pki/private/ca.key" -out "$data_dir/pki/ca.crt" >/dev/null 2>&1
  openssl req -newkey rsa:2048 -nodes -subj "/CN=$OVPN_SERVER_NAME" -keyout "$data_dir/pki/private/$OVPN_SERVER_NAME.key" -out "$data_dir/server.csr" >/dev/null 2>&1
  printf 'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n' >"$data_dir/server.ext"
  openssl x509 -req -in "$data_dir/server.csr" -CA "$data_dir/pki/ca.crt" -CAkey "$data_dir/pki/private/ca.key" -CAcreateserial -days 1 -out "$data_dir/pki/issued/$OVPN_SERVER_NAME.crt" -extfile "$data_dir/server.ext" >/dev/null 2>&1
  cat >"$data_dir/crl.cnf" <<CRL_CONFIG
[ ca ]
default_ca = CA_default
[ CA_default ]
database = $data_dir/ca-db/index.txt
new_certs_dir = $data_dir/ca-db/newcerts
certificate = $data_dir/pki/ca.crt
private_key = $data_dir/pki/private/ca.key
serial = $data_dir/ca-db/serial
crlnumber = $data_dir/ca-db/crlnumber
default_md = sha256
default_crl_days = 1
policy = policy_any
[ policy_any ]
commonName = supplied
CRL_CONFIG
  openssl ca -config "$data_dir/crl.cnf" -gencrl -out "$data_dir/pki/crl.pem" >/dev/null 2>&1
  fingerprint="$(openssl x509 -in "$data_dir/pki/ca.crt" -noout -fingerprint -sha256)"
  fingerprint="${fingerprint#*=}"
  printf '{\n  "ca_fingerprint_sha256": "%s"\n}\n' "$fingerprint" >"$data_dir/meta/instance.json"
}

assert_state() {
  local data_dir="$1"
  local expected="$2"

  OVPN_DATA_DIR="$data_dir"
  ovpn_state_scan
  if [ "$OVPN_STATE" != "$expected" ]; then
    echo "expected $expected, got $OVPN_STATE for $data_dir" >&2
    exit 1
  fi
}

assert_issue() {
  local wanted="$1"
  local issue

  for issue in "${OVPN_STATE_ISSUE_IDS[@]}"; do
    [ "$issue" = "$wanted" ] && return 0
  done
  echo "missing state issue $wanted" >&2
  exit 1
}

healthy="$TMP_DIR/healthy"
make_crypto_healthy "$healthy"
assert_state "$healthy" HEALTHY

server_key_mismatch="$TMP_DIR/server-key-mismatch"
make_crypto_healthy "$server_key_mismatch"
openssl genpkey -algorithm RSA -out "$server_key_mismatch/pki/private/$OVPN_SERVER_NAME.key" >/dev/null 2>&1
assert_state "$server_key_mismatch" CRITICAL
assert_issue SERVER_CERT_KEY_MISMATCH

invalid_crl="$TMP_DIR/invalid-crl"
make_crypto_healthy "$invalid_crl"
printf 'not a CRL\n' >"$invalid_crl/pki/crl.pem"
assert_state "$invalid_crl" DEGRADED_REPAIRABLE
assert_issue CRL_INVALID

metadata_mismatch="$TMP_DIR/metadata-mismatch"
make_crypto_healthy "$metadata_mismatch"
printf '{\n  "ca_fingerprint_sha256": "00:11:22"\n}\n' >"$metadata_mismatch/meta/instance.json"
assert_state "$metadata_mismatch" CRITICAL
assert_issue METADATA_CA_FINGERPRINT_MISMATCH

unrecoverable="$TMP_DIR/unrecoverable"
make_crypto_healthy "$unrecoverable"
rm "$unrecoverable/pki/private/ca.key"
assert_state "$unrecoverable" UNRECOVERABLE
set +e
OVPN_DATA_DIR="$unrecoverable" "$OVPN" start >"$TMP_DIR/unrecoverable-start.out" 2>"$TMP_DIR/unrecoverable-start.err"
status=$?
set -e
if [ "$status" -ne 78 ]; then
  echo "UNRECOVERABLE start returned $status instead of 78" >&2
  exit 1
fi
grep -Fq 'instance state is UNRECOVERABLE' "$TMP_DIR/unrecoverable-start.err"

printf 'crypto state smoke passed\n'
