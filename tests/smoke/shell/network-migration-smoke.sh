#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
SOCKET_LISTENER_PID=''
mkdir -p "$FAKE_BIN"

cleanup() {
  [ -z "$SOCKET_LISTENER_PID" ] || kill "$SOCKET_LISTENER_PID" >/dev/null 2>&1 || true
  [ -z "$SOCKET_LISTENER_PID" ] || wait "$SOCKET_LISTENER_PID" 2>/dev/null || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

cat >"$FAKE_BIN/openvpn" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = --version ]; then
  printf 'OpenVPN 2.7.5 test-build\n'
fi
EOF
cat >"$FAKE_BIN/socat" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
request="$(cat)"
printf '%s\n' "$request" >>"$OVPN_TEST_SOCAT_LOG"
case "$request" in
  *broker-health*) printf 'SUCCESS: broker connected to OpenVPN\n' ;;
  *'signal SIGHUP'*) printf 'SUCCESS: signal SIGHUP thrown\n' ;;
  *version*) printf 'OpenVPN Version: OpenVPN 2.7.5 test-build\nEND\n' ;;
  *) printf 'SUCCESS: command accepted\n' ;;
esac
EOF
cat >"$FAKE_BIN/pgrep" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "${1:-}" = -x ] && [ "${2:-}" = openvpn ]
EOF
chmod +x "$FAKE_BIN/openvpn" "$FAKE_BIN/socat" "$FAKE_BIN/pgrep"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_MANAGEMENT_SOCKET="$OVPN_RUNTIME_DIR/management.sock"
export OVPN_RUNTIME_STATE_FILE="$OVPN_RUNTIME_DIR/state.json"
export OVPN_SOCAT_BIN="$FAKE_BIN/socat"
export OVPN_TEST_SOCAT_LOG="$TMP_DIR/socat.log"
export OVPN_LEASE_DIR="$TMP_DIR/leases"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"
export PATH="$FAKE_BIN:$PATH"

mkdir -p "$OVPN_RUNTIME_DIR"
nc -lU "$OVPN_MANAGEMENT_SOCKET" >/dev/null 2>&1 &
SOCKET_LISTENER_PID=$!
for _attempt in {1..20}; do
  [ -S "$OVPN_MANAGEMENT_SOCKET" ] && break
  sleep 0.1
done
[ -S "$OVPN_MANAGEMENT_SOCKET" ] || {
  echo 'failed to create a UNIX management socket fixture' >&2
  exit 1
}
cat >"$OVPN_RUNTIME_STATE_FILE" <<'EOF'
{
  "service": "openvpn",
  "instance_state": "HEALTHY",
  "daemon": "running",
  "maintenance": false
}
EOF

"$OVPN" config apply
mkdir -p "$OVPN_DATA_DIR/data" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/pki" "$OVPN_LEASE_DIR"
printf '%s\n' \
  $'V\t30000101000000Z\t\t01\tunknown\t/CN=11111111-1111-4111-8111-111111111111' \
  $'V\t30000101000000Z\t\t02\tunknown\t/CN=22222222-2222-4222-8222-222222222222' \
  >"$OVPN_DATA_DIR/pki/index.txt"
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'EOF'
# id,name,ip
11111111-1111-4111-8111-111111111111,alpha,10.88.0.20
22222222-2222-4222-8222-222222222222,bravo,
EOF
cp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
printf '%s\n' '# id,name,state' \
  '11111111-1111-4111-8111-111111111111,alpha,active' \
  '22222222-2222-4222-8222-222222222222,bravo,active' >"$OVPN_DATA_DIR/meta/client-state.csv"
: >"$OVPN_DATA_DIR/meta/audit.jsonl"
printf '10.88.0.200\n' >"$OVPN_LEASE_DIR/22222222-2222-4222-8222-222222222222"
"$OVPN" client ip set alpha --ip 10.88.0.20

"$OVPN" network plan --network 10.89.0.0/24 --dynamic-pool-size 100 >"$TMP_DIR/dry.out"
grep -Eq 'Network:[[:space:]]+10\.88\.0\.0/24[[:space:]]+->[[:space:]]+10\.89\.0\.0/24' "$TMP_DIR/dry.out"
grep -Fqx 'OVPN_NETWORK=10.88.0.0/24' "$OVPN_DATA_DIR/config/project.env"

"$OVPN" network apply --network 10.89.0.0/24 --dynamic-pool-size 100 --yes >"$TMP_DIR/apply.out"
grep -Fqx 'OVPN_NETWORK=10.89.0.0/24' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_DYNAMIC_POOL_SIZE=100' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx '11111111-1111-4111-8111-111111111111,alpha,10.89.0.20' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'ifconfig-push 10.89.0.20 255.255.255.0' "$OVPN_DATA_DIR/ccd/11111111-1111-4111-8111-111111111111"
test -z "$(ls -A "$OVPN_LEASE_DIR" 2>/dev/null)"
grep -q '^server 10.89.0.0 255.255.255.0 nopool$' "$OVPN_DATA_DIR/server/server.conf"
grep -Fq 'signal SIGHUP' "$OVPN_TEST_SOCAT_LOG"
grep -Fq '"event":"network_migration","outcome":"applied"' "$OVPN_DATA_DIR/meta/audit.jsonl"
jq -e -s '
  any(.[]; .event == "network_migration" and .operation == "apply" and
    .outcome == "applied" and .from_network == "10.88.0.0/24" and
    .to_network == "10.89.0.0/24" and .from_dynamic_pool == 126 and
    .to_dynamic_pool == 100)
' "$OVPN_DATA_DIR/logs/events.jsonl" >/dev/null

if OVPN_NETWORK_MIGRATION_FAIL_HEALTH=true "$OVPN" network apply --network 10.90.0.0/24 --yes >"$TMP_DIR/fail.out" 2>"$TMP_DIR/fail.err"; then
  echo 'failed network migration unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'network migration rollback completed; OpenVPN is healthy' "$TMP_DIR/fail.err"
grep -Fqx 'OVPN_NETWORK=10.89.0.0/24' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx '11111111-1111-4111-8111-111111111111,alpha,10.89.0.20' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fq '"event":"network_migration","outcome":"rejected"' "$OVPN_DATA_DIR/meta/audit.jsonl"
jq -e -s '
  any(.[]; .event == "network_migration" and .operation == "apply" and
    .outcome == "rejected" and .from_network == "10.89.0.0/24" and
    .to_network == "10.90.0.0/24")
' "$OVPN_DATA_DIR/logs/events.jsonl" >/dev/null

kill "$SOCKET_LISTENER_PID" >/dev/null 2>&1 || true
wait "$SOCKET_LISTENER_PID" 2>/dev/null || true
SOCKET_LISTENER_PID=''
rm -f "$OVPN_MANAGEMENT_SOCKET"
before_config="$(sha256sum "$OVPN_DATA_DIR/config/project.env")"
if "$OVPN" network apply --network 10.90.0.0/24 --yes >"$TMP_DIR/offline.out" 2>"$TMP_DIR/offline.err"; then
  echo 'offline network migration unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'requires a healthy running OpenVPN process and management socket' "$TMP_DIR/offline.err"
[ "$before_config" = "$(sha256sum "$OVPN_DATA_DIR/config/project.env")" ] || {
  echo 'offline network migration changed persistent configuration' >&2
  exit 1
}

printf 'network migration smoke passed\n'
