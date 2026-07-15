#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
maintenance_pid=''

cleanup() {
  if [ -n "$maintenance_pid" ]; then
    kill -TERM "$maintenance_pid" >/dev/null 2>&1 || true
    wait "$maintenance_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"
export OVPN_SERVER_NAME=openvpn-server

make_critical() {
  local data_dir="$1"

  mkdir -p "$data_dir/config" "$data_dir/meta" "$data_dir/server" "$data_dir/pki/private" "$data_dir/pki/issued" "$data_dir/secrets"
  : >"$data_dir/config/project.env"
  : >"$data_dir/config/schema-version"
  printf '{\n  "ca_fingerprint_sha256": "FAKE:CA:FINGERPRINT"\n}\n' >"$data_dir/meta/instance.json"
  : >"$data_dir/pki/ca.crt"
  : >"$data_dir/pki/private/ca.key"
  : >"$data_dir/pki/issued/$OVPN_SERVER_NAME.crt"
  : >"$data_dir/pki/private/$OVPN_SERVER_NAME.key"
  : >"$data_dir/pki/serial"
  : >"$data_dir/pki/crl.pem"
  : >"$data_dir/secrets/tls-crypt.key"
  : >"$data_dir/server/server.conf"
}

critical="$TMP_DIR/critical"
runtime_dir="$TMP_DIR/runtime"
make_critical "$critical"

set +e
OVPN_DATA_DIR="$critical" OVPN_RUNTIME_DIR="$runtime_dir" OVPN_CRITICAL_MODE=exit "$OVPN" start >"$TMP_DIR/exit.out" 2>"$TMP_DIR/exit.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'recommended: docker compose run --rm openvpn-maintenance state doctor' "$TMP_DIR/exit.err"

OVPN_DATA_DIR="$critical" OVPN_RUNTIME_DIR="$runtime_dir" "$OVPN" runtime status >"$TMP_DIR/fallback-status.json"
grep -Fq '"instance_state": "CRITICAL"' "$TMP_DIR/fallback-status.json"
grep -Fq '"daemon": "unknown"' "$TMP_DIR/fallback-status.json"

set +e
OVPN_DATA_DIR="$critical" OVPN_RUNTIME_DIR="$runtime_dir" OVPN_CRITICAL_MODE=invalid "$OVPN" start >"$TMP_DIR/invalid.out" 2>"$TMP_DIR/invalid.err"
status=$?
set -e
[ "$status" -eq 1 ]
grep -Fq 'OVPN_CRITICAL_MODE must be exit or maintenance' "$TMP_DIR/invalid.err"

OVPN_DATA_DIR="$critical" OVPN_RUNTIME_DIR="$runtime_dir" OVPN_CRITICAL_MODE=maintenance "$OVPN" start >"$TMP_DIR/maintenance.out" 2>"$TMP_DIR/maintenance.err" &
maintenance_pid=$!
for _ in $(seq 1 30); do
  [ -f "$runtime_dir/state.json" ] && break
  sleep 0.1
done
[ -f "$runtime_dir/state.json" ]
OVPN_DATA_DIR="$critical" OVPN_RUNTIME_DIR="$runtime_dir" "$OVPN" runtime status >"$TMP_DIR/maintenance-status.json"
grep -Fq '"instance_state": "CRITICAL"' "$TMP_DIR/maintenance-status.json"
grep -Fq '"daemon": "stopped"' "$TMP_DIR/maintenance-status.json"
grep -Fq '"maintenance": true' "$TMP_DIR/maintenance-status.json"
set +e
OVPN_DATA_DIR="$critical" OVPN_RUNTIME_DIR="$runtime_dir" "$OVPN" runtime health >"$TMP_DIR/health.out" 2>"$TMP_DIR/health.err"
status=$?
set -e
[ "$status" -eq 1 ]
grep -Fq 'instance is in maintenance mode' "$TMP_DIR/health.err"
kill -TERM "$maintenance_pid"
wait "$maintenance_pid"
maintenance_pid=''

printf 'maintenance smoke passed\n'
