#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"
export OVPN_SERVER_NAME=openvpn-server
# shellcheck source=../rootfs/usr/local/lib/openvpn-container/common.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/common.sh"
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/state.sh"

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
make_healthy "$healthy"
before="$(find "$healthy" -printf '%P:%y\n' | sort)"
assert_state "$healthy" HEALTHY
after="$(find "$healthy" -printf '%P:%y\n' | sort)"
[ "$before" = "$after" ] || {
  echo 'state scanner modified a healthy fixture' >&2
  exit 1
}

repairable="$TMP_DIR/repairable"
make_healthy "$repairable"
rm "$repairable/server/server.conf"
assert_state "$repairable" DEGRADED_REPAIRABLE
assert_issue SERVER_CONFIG_MISSING

recoverable="$TMP_DIR/recoverable"
make_healthy "$recoverable"
mkdir -p "$recoverable/clients/active"
: >"$recoverable/clients/active/laptop.ovpn"
rm "$recoverable/pki/ca.crt"
assert_state "$recoverable" DEGRADED_RECOVERABLE
assert_issue CA_CERT_MISSING

reissuable="$TMP_DIR/reissuable"
make_healthy "$reissuable"
rm "$reissuable/pki/issued/$OVPN_SERVER_NAME.crt"
assert_state "$reissuable" DEGRADED_REISSUABLE
assert_issue SERVER_CERT_MISSING

critical="$TMP_DIR/critical"
make_healthy "$critical"
rm "$critical/pki/index.txt"
assert_state "$critical" CRITICAL
assert_issue PKI_INDEX_MISSING

unrecoverable="$TMP_DIR/unrecoverable"
make_healthy "$unrecoverable"
rm "$unrecoverable/pki/private/ca.key"
assert_state "$unrecoverable" UNRECOVERABLE
assert_issue CA_KEY_MISSING

interrupted="$TMP_DIR/interrupted"
make_healthy "$interrupted"
: >"$interrupted/.init-transaction"
assert_state "$interrupted" CRITICAL
assert_issue INITIALIZATION_INTERRUPTED

printf 'state machine smoke passed\n'
