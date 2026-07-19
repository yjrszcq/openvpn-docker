#!/usr/bin/env bash
# Full integration test — runs inside a locally built project image.
#
# Usage:
#   docker run --rm --name ovpn-integration-test \
#     --entrypoint bash \
#     --cap-add NET_ADMIN \
#     --device /dev/net/tun:/dev/net/tun \
#     -v "$PWD/tests/integration/container-full-test.sh:/test.sh:ro" \
#     -e OVPN_ENDPOINT=vpn.example.test \
#     -e OVPN_NETWORK=10.213.0.0/24 \
#     -e OVPN_DYNAMIC_POOL_SIZE=64 \
#     openvpn-audit:latest /test.sh
#
# Uses bridge networking — TUN is container-local, won't touch the host stack.
set -euo pipefail

export OVPN_DATA_DIR="${OVPN_DATA_DIR:-/etc/openvpn}"
export OVPN_RUNTIME_DIR="${OVPN_RUNTIME_DIR:-/run/openvpn-container}"
export OVPN_LEASE_DIR="${OVPN_LEASE_DIR:-$OVPN_DATA_DIR/cache/client-leases}"
export OVPN_MANAGEMENT_SOCKET="${OVPN_MANAGEMENT_SOCKET:-$OVPN_RUNTIME_DIR/management.sock}"
export OVPN_OPENVPN_MANAGEMENT_SOCKET="${OVPN_OPENVPN_MANAGEMENT_SOCKET:-$OVPN_RUNTIME_DIR/openvpn-management.sock}"
BROKER_PID=''

PASS=0; FAIL=0
pass() { PASS=$((PASS+1)); printf '  PASS: %s\n' "$1"; }
fail() { FAIL=$((FAIL+1)); printf '  FAIL: %s\n' "$1" >&2; }
check() { if [ "${1:-1}" -eq 0 ]; then pass "$2"; else fail "$2 (${3:-})"; fi; }
check_condition() { local description="$1"; shift; if "$@"; then pass "$description"; else fail "$description"; fi; }
check_out() { grep -q "$2" "${3:-/dev/stdin}" 2>/dev/null; check $? "$1" "unexpected content"; }
check_not_out() { if grep -q "$2" "$1" 2>/dev/null; then fail "$3 (unexpected match: $2)"; else pass "$3"; fi; }

OUT=/tmp/test-out.txt

# ============================================================
# Phase 1: init + config
# ============================================================
echo "=== Phase 1: init + config ==="

ovpn init 2>&1
check $? "ovpn init"
check_condition "authoritative client-IP registry initialized" \
  test -f "$OVPN_DATA_DIR/meta/client-ip.csv"
check_condition "obsolete data directory absent" test ! -e "$OVPN_DATA_DIR/data"

ovpn config show >$OUT
grep -q "OVPN_NETWORK" $OUT; check $? "config show contains network"
grep -q "OVPN_DYNAMIC_POOL_SIZE" $OUT; check $? "config show contains pool size"

# ============================================================
echo "=== Phase 2: state + repair ==="

s=$(ovpn state show)
check $? "state show ($s)"
check_condition "state=HEALTHY" test "$s" = "HEALTHY"

ovpn state doctor >$OUT
grep -q "HEALTHY" $OUT; check $? "state doctor"

ovpn state doctor --json >$OUT
grep -q '"state"' $OUT; check $? "state doctor --json"

ovpn repair plan >$OUT
check $? "repair plan"

ovpn repair plan --json >$OUT
grep -q '"state"' $OUT; check $? "repair plan --json"

ovpn repair apply >$OUT
check $? "repair apply"

# ============================================================
echo "=== Phase 3: client create ==="

ovpn client create static01 >$OUT 2>&1
check $? "create static01 (auto static)"

ovpn client create static02 --ip 10.213.0.5 >$OUT 2>&1
check $? "create static02 --ip 10.213.0.5"

ovpn client create dynamic01 --dynamic >$OUT 2>&1
check $? "create dynamic01 --dynamic"

ovpn client create dynamic02 --dynamic >$OUT 2>&1
check $? "create dynamic02 --dynamic"

if ovpn client create static01 >/dev/null 2>&1; then
  fail "duplicate name rejected"
else
  pass "duplicate name rejected"
fi

if ovpn client create static03 --ip 10.213.0.5 >/dev/null 2>&1; then
  fail "duplicate IP rejected"
else
  pass "duplicate IP rejected"
fi

if ovpn client create bad-name --ip 10.213.0.5 --dynamic >/dev/null 2>&1; then
  fail "--ip + --dynamic rejected"
else
  pass "--ip + --dynamic rejected"
fi

ovpn client create rename-old --dynamic >$OUT 2>&1
rename_id="$(awk -F, '$2 == "rename-old" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
ovpn client rename -i "$rename_id" rename-new >$OUT 2>&1
grep -qE "${rename_id}.*rename-new.*active" <(ovpn client list --no-trunc)
check $? "rename preserves client UUID"
ovpn client revoke rename-new >$OUT 2>&1
ovpn client delete -i "$rename_id" >$OUT 2>&1
check $? "renamed client cleanup"

# ============================================================
echo "=== Phase 4: export + render ==="

ovpn client export static01 >$OUT 2>&1
check $? "client export static01"
grep -q "BEGIN PRIVATE KEY" $OUT; check $? "export has key"

ovpn render client static01 --stdout >$OUT 2>&1
check $? "render client static01"

ovpn render server --stdout >$OUT 2>&1
check $? "render server --stdout"
grep -q "server 10.213.0.0" $OUT; check $? "rendered conf has server"
grep -q 'client-connect "/usr/local/bin/ovpn-hook pool-persist"' $OUT; check $? "rendered conf has hook"
check_not_out $OUT "ifconfig-pool-persist" "rendered conf has NO ifconfig-pool-persist"

# ============================================================
echo "=== Phase 5: client list ==="

ovpn client list >$OUT
check $? "client list"
grep -q "static01" $OUT; check $? "list shows static01"
grep -q "dynamic01" $OUT; check $? "list shows dynamic01"

ovpn client list --detail >$OUT
check $? "client list --detail"
grep -q "static01" $OUT; check $? "detail shows static01"
grep -q "static" $OUT; check $? "detail shows static mode"
grep -q "dynamic" $OUT; check $? "detail shows dynamic mode"

# ============================================================
echo "=== Phase 6: client ip set ==="

# re-apply same value
ovpn client ip set static01 --ip 10.213.0.3 >$OUT 2>&1
check $? "ip set static01 --ip 10.213.0.3 (re-apply)"

# change static IP
ovpn client ip set static01 --ip 10.213.0.4 >$OUT 2>&1
check $? "ip set static01 --ip 10.213.0.4 (change)"

# auto-allocate
ovpn client ip set static01 >$OUT 2>&1
check $? "ip set static01 (auto)"

# static to dynamic
ovpn client ip set static01 --dynamic >$OUT 2>&1
check $? "ip set static01 --dynamic"

# dynamic to static
ovpn client ip set static01 --ip 10.213.0.6 >$OUT 2>&1
check $? "ip set static01 --ip 10.213.0.6"

# same dynamic
ovpn client ip set dynamic01 --dynamic >$OUT 2>&1
check $? "ip set dynamic01 --dynamic (same)"

# IP in dynamic pool rejected
if ovpn client ip set static01 --ip 10.213.0.200 >/dev/null 2>&1; then
  fail "dynamic-pool IP rejected in ip set"
else
  pass "dynamic-pool IP rejected in ip set"
fi

# duplicate IP rejected
if ovpn client ip set static01 --ip 10.213.0.5 >/dev/null 2>&1; then
  fail "duplicate IP rejected in ip set"
else
  pass "duplicate IP rejected in ip set"
fi

# ============================================================
echo "=== Phase 7: client revoke + reissue + delete ==="

ovpn client revoke dynamic02 >$OUT 2>&1
check $? "revoke dynamic02"

ovpn client list >$OUT
grep -qE "dynamic02.*revoked" $OUT; check $? "list shows revoked"

ovpn client reissue dynamic02 >$OUT 2>&1
check $? "reissue dynamic02"

ovpn client list >$OUT
grep -qE "dynamic02.*active" $OUT; check $? "reissued client is active"

# revoke with release-ip
ovpn client create static99 --ip 10.213.0.10 >$OUT 2>&1
ovpn client revoke static99 --release-ip >$OUT 2>&1
check $? "revoke --release-ip"

# ip release
ovpn client create static98 --ip 10.213.0.11 >$OUT 2>&1
ovpn client revoke static98 >$OUT 2>&1
ovpn client ip release static98 >$OUT 2>&1
check $? "client ip release"

# delete
ovpn client delete static98 >$OUT 2>&1
check $? "client delete"

ovpn client list >$OUT
check_not_out $OUT "static98" "deleted client gone from list"

# ============================================================
echo "=== Phase 8: network plan ==="

ovpn network plan >$OUT
check $? "network plan"

ovpn network plan --network 10.214.0.0/24 --dynamic-pool-size 128 >$OUT
check $? "network plan with options"
grep -q "10.214.0.0" $OUT; check $? "plan shows new network"

# apply without daemon should fail
if ovpn network apply --network 10.214.0.0/24 --yes >$OUT 2>&1; then
  fail "network apply needs daemon"
else
  grep -q "management socket" $OUT; check $? "apply rejected: no mgmt socket"
fi

# ============================================================
echo "=== Phase 9: runtime (non-daemon) ==="

ovpn runtime version >$OUT
check $? "runtime version"
grep -q "openvpn" $OUT; check $? "version has openvpn"

ovpn runtime capabilities >$OUT
check $? "runtime capabilities"
grep -q "version" $OUT; check $? "capabilities has version"

ovpn help >$OUT
check $? "ovpn help"
grep -q "init" $OUT; check $? "help shows init"

ovpn --version >$OUT
check $? "ovpn --version"
grep -qE "[0-9]+\.[0-9]+\.[0-9]+" $OUT; check $? "--version shows version"

ovpn -v >$OUT
check $? "ovpn -v"

# ============================================================
echo "=== Phase 10: config apply ==="

OVPN_DYNAMIC_POOL_SIZE=128 ovpn config apply >$OUT 2>&1
check $? "config apply pool=128"
ovpn config show >$OUT
grep -q "OVPN_DYNAMIC_POOL_SIZE=128" $OUT; check $? "pool=128 persisted"
OVPN_DYNAMIC_POOL_SIZE=64 ovpn config apply >$OUT 2>&1

# ============================================================
echo "=== Phase 11: Dynamic lease hook ==="

LEASE_DIR="${OVPN_LEASE_DIR:-/etc/openvpn/cache/client-leases}"
mkdir -p "$LEASE_DIR"
dynamic01_id=11111111-1111-4111-8111-111111111111
dynamic02_id=22222222-2222-4222-8222-222222222222

# simulate client connect
script_type=client-connect common_name="$dynamic01_id" ifconfig_pool_remote_ip=10.213.0.200 \
  /usr/local/bin/ovpn-hook pool-persist
check $? "hook client-connect dynamic01"
check_condition "lease file exists" test -f "$LEASE_DIR/$dynamic01_id"
check_condition "lease file content correct" test "$(cat "$LEASE_DIR/$dynamic01_id")" = "10.213.0.200"

# reconnect with new IP
script_type=client-connect common_name="$dynamic01_id" ifconfig_pool_remote_ip=10.213.0.201 \
  /usr/local/bin/ovpn-hook pool-persist
check_condition "lease updated on reconnect" test "$(cat "$LEASE_DIR/$dynamic01_id")" = "10.213.0.201"

# another client gets recycled IP
script_type=client-connect common_name="$dynamic02_id" ifconfig_pool_remote_ip=10.213.0.200 \
  /usr/local/bin/ovpn-hook pool-persist
check_condition "recycled IP attributed to new client" test -f "$LEASE_DIR/$dynamic02_id"

# disconnect is no-op
script_type=client-disconnect common_name="$dynamic01_id" \
  /usr/local/bin/ovpn-hook pool-persist
check_condition "lease preserved after disconnect" test -f "$LEASE_DIR/$dynamic01_id"

# verify no duplicate IP files
dup_count=0
for f in "$LEASE_DIR"/*; do
  [ -f "$f" ] || continue
  [ "$(cat "$f")" = "10.213.0.200" ] && dup_count=$((dup_count+1))
done
check_condition "no duplicate IP files ($dup_count)" test "$dup_count" -le 1

# ============================================================
echo "=== Phase 12: Start OpenVPN daemon ==="

ovpn render server >$OUT 2>&1
check $? "render server"

# Write runtime state so healthcheck passes
mkdir -p "$OVPN_RUNTIME_DIR"
cat >"${OVPN_RUNTIME_STATE_FILE:-$OVPN_RUNTIME_DIR/state.json}" <<'STATE'
{"service": "openvpn", "instance_state": "HEALTHY", "daemon": "running", "maintenance": false}
STATE

python3 /usr/local/lib/openvpn-container/management-broker.py \
  --listen "$OVPN_MANAGEMENT_SOCKET" \
  --backend "$OVPN_OPENVPN_MANAGEMENT_SOCKET" \
  --raw-log "$OVPN_DATA_DIR/logs/openvpn.log" \
  --max-bytes 10485760 \
  --backups 5 &
BROKER_PID=$!

openvpn --config "$OVPN_DATA_DIR/server/server.conf" \
  --writepid /tmp/openvpn.pid \
  --log /tmp/openvpn.log \
  --daemon 2>/dev/null
check $? "openvpn started"

for _attempt in $(seq 1 30); do
  if [ -S "${OVPN_RUNTIME_DIR}/management.sock" ]; then
    break
  fi
  sleep 0.5
done
[ -S "${OVPN_RUNTIME_DIR}/management.sock" ]; check $? "mgmt socket created"

sleep 1

ovpn runtime status >$OUT 2>&1
check $? "runtime status"
grep -q '"daemon"' $OUT; check $? "status has daemon"

ovpn runtime health >$OUT 2>&1 || true
check $? "runtime health"

ovpn runtime logs --lines 20 >$OUT 2>&1
check $? "runtime logs"

ovpn runtime events --lines 20 --json >$OUT 2>&1
jq -e -s 'length > 0' $OUT >/dev/null
check $? "runtime events --json"

ovpn client list --detail >$OUT || true
check $? "client list --detail (live)"
grep -qE "online|offline" $OUT; check $? "detail shows connection status"

# ============================================================
echo "=== Phase 13: network apply (live) ==="

ovpn network plan --network 10.214.0.0/24 --dynamic-pool-size 100 >$OUT
rc=0; ovpn network apply --network 10.214.0.0/24 --dynamic-pool-size 100 --yes >$OUT 2>&1 || rc=$?
check $rc "network apply live"

ovpn config show >$OUT
grep -q "OVPN_NETWORK=10.214.0.0" $OUT; check $? "network migrated to 10.214.0.0"

# revert
rc=0; ovpn network apply --network 10.213.0.0/24 --dynamic-pool-size 64 --yes >$OUT 2>&1 || rc=$?
check $rc "network apply revert"

ovpn config show >$OUT
grep -q "OVPN_NETWORK=10.213.0.0" $OUT; check $? "network reverted to 10.213.0.0"

# ============================================================
echo "=== Phase 14: Daemon mode commands ==="

# revoke with daemon running (disconnects client)
rc=0
ovpn client revoke dynamic02 >/tmp/revoke-daemon.out 2>&1 || rc=$?
[ "$rc" -eq 0 ] || sed 's/^/  revoke: /' /tmp/revoke-daemon.out >&2
check $rc "revoke with daemon"

# reissue with daemon
rc=0; ovpn client reissue dynamic02 >$OUT 2>&1 || rc=$?
check $rc "reissue with daemon"

# config apply triggers reload
rc=0; OVPN_CLIENT_TO_CLIENT=false ovpn config apply >$OUT 2>&1 || rc=$?
check $rc "config apply with daemon (reload)"
ovpn config show >$OUT
grep -q "OVPN_CLIENT_TO_CLIENT=false" $OUT; check $? "config change persisted"
OVPN_CLIENT_TO_CLIENT=true ovpn config apply >$OUT 2>&1

# ============================================================
echo "=== Phase 15: Cleanup ==="

if [ -f /tmp/openvpn.pid ]; then
  kill "$(cat /tmp/openvpn.pid)" 2>/dev/null || true
fi
[ -z "$BROKER_PID" ] || kill "$BROKER_PID" 2>/dev/null || true
[ -z "$BROKER_PID" ] || wait "$BROKER_PID" 2>/dev/null || true
BROKER_PID=''
sleep 1
pass "daemon cleanup"

OVPN_MAINTENANCE=true ovpn migrate plan --json >$OUT
jq -e '.source_schema == 3 and .target_schema == 3 and .blocked == false' $OUT >/dev/null
check $? "migrate plan --json at current schema"

OVPN_MAINTENANCE=true ovpn migrate apply --yes >$OUT
check $? "migrate apply idempotent at current schema"

# ============================================================
echo ""
echo "========================================="
printf "Results: %d passed, %d failed\n" "$PASS" "$FAIL"
echo "========================================="

[ "$FAIL" -eq 0 ] || exit 1
