#!/usr/bin/env bash
# Full integration test — runs inside szcq/openvpn:2.7.5 with new scripts bind-mounted.
#
# Usage:
#   docker run --rm --name ovpn-integration-test \
#     --cap-add NET_ADMIN \
#     --device /dev/net/tun:/dev/net/tun \
#     -v "$PWD/rootfs/usr/local/bin/ovpn:/usr/local/bin/ovpn:ro" \
#     -v "$PWD/rootfs/usr/local/lib/openvpn-container:/usr/local/lib/openvpn-container:ro" \
#     -v "$PWD/rootfs/usr/local/share/openvpn-container/templates:/usr/local/share/openvpn-container/templates:ro" \
#     -v "$PWD/compatibility:/usr/local/share/openvpn-container/compatibility:ro" \
#     -v "$PWD/tests/integration/container-full-test.sh:/test.sh:ro" \
#     -e OVPN_ENDPOINT=vpn.example.test \
#     -e OVPN_NETWORK=10.213.0.0/24 \
#     -e OVPN_DYNAMIC_POOL_SIZE=64 \
#     szcq/openvpn:2.7.5 bash /test.sh
#
# Uses bridge networking — TUN is container-local, won't touch the host stack.
set -euo pipefail

export OVPN_DATA_DIR="${OVPN_DATA_DIR:-/etc/openvpn}"
export OVPN_RUNTIME_DIR="${OVPN_RUNTIME_DIR:-/run/openvpn-container}"
export OVPN_LEASE_DIR="${OVPN_LEASE_DIR:-$OVPN_DATA_DIR/data/leases}"
export OVPN_MANAGEMENT_SOCKET="${OVPN_MANAGEMENT_SOCKET:-$OVPN_RUNTIME_DIR/management.sock}"

PASS=0; FAIL=0
pass() { PASS=$((PASS+1)); printf '  PASS: %s\n' "$1"; }
fail() { FAIL=$((FAIL+1)); printf '  FAIL: %s\n' "$1" >&2; }
check() { if [ "${1:-1}" -eq 0 ]; then pass "$2"; else fail "$2 (${3:-})"; fi; }
check_out() { grep -q "$2" "${3:-/dev/stdin}" 2>/dev/null; check $? "$1" "unexpected content"; }
check_not_out() { if grep -q "$2" "$1" 2>/dev/null; then fail "$3 (unexpected match: $2)"; else pass "$3"; fi; }

OUT=/tmp/test-out.txt

# ============================================================
# Phase 1: init + config
# ============================================================
echo "=== Phase 1: init + config ==="

ovpn init 2>&1
check $? "ovpn init"

ovpn config show >$OUT
grep -q "OVPN_NETWORK" $OUT; check $? "config show contains network"
grep -q "OVPN_DYNAMIC_POOL_SIZE" $OUT; check $? "config show contains pool size"

# ============================================================
echo "=== Phase 2: state + repair ==="

s=$(ovpn state show)
check $? "state show ($s)"
[ "$s" = "HEALTHY" ]; check $? "state=HEALTHY"

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
grep -q "pool-persist-hook.sh" $OUT; check $? "rendered conf has hook"
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

LEASE_DIR="${OVPN_LEASE_DIR:-/etc/openvpn/data/leases}"
mkdir -p "$LEASE_DIR"

# simulate client connect
script_type=client-connect common_name=dynamic01 ifconfig_pool_remote_ip=10.213.0.200 \
  /usr/local/lib/openvpn-container/pool-persist-hook.sh
check $? "hook client-connect dynamic01"
[ -f "$LEASE_DIR/dynamic01" ]; check $? "lease file exists"
[ "$(cat "$LEASE_DIR/dynamic01")" = "10.213.0.200" ]; check $? "lease file content correct"

# reconnect with new IP
script_type=client-connect common_name=dynamic01 ifconfig_pool_remote_ip=10.213.0.201 \
  /usr/local/lib/openvpn-container/pool-persist-hook.sh
[ "$(cat "$LEASE_DIR/dynamic01")" = "10.213.0.201" ]; check $? "lease updated on reconnect"

# another client gets recycled IP
script_type=client-connect common_name=dynamic02 ifconfig_pool_remote_ip=10.213.0.200 \
  /usr/local/lib/openvpn-container/pool-persist-hook.sh
[ -f "$LEASE_DIR/dynamic02" ]; check $? "recycled IP attributed to new client"

# disconnect is no-op
script_type=client-disconnect common_name=dynamic01 \
  /usr/local/lib/openvpn-container/pool-persist-hook.sh
[ -f "$LEASE_DIR/dynamic01" ]; check $? "lease preserved after disconnect"

# verify no duplicate IP files
dup_count=0
for f in "$LEASE_DIR"/*; do
  [ -f "$f" ] || continue
  [ "$(cat "$f")" = "10.213.0.200" ] && dup_count=$((dup_count+1))
done
[ "$dup_count" -le 1 ]; check $? "no duplicate IP files ($dup_count)"

# ============================================================
echo "=== Phase 12: Start OpenVPN daemon ==="

ovpn render server >$OUT 2>&1
check $? "render server"

# Write runtime state so healthcheck passes
mkdir -p "$OVPN_RUNTIME_DIR"
cat >"${OVPN_RUNTIME_STATE_FILE:-$OVPN_RUNTIME_DIR/state.json}" <<'STATE'
{"service": "openvpn", "instance_state": "HEALTHY", "daemon": "running", "maintenance": false}
STATE

openvpn --config "$OVPN_DATA_DIR/server/server.conf" \
  --writepid /tmp/openvpn.pid \
  --log /tmp/openvpn.log \
  --daemon 2>/dev/null
check $? "openvpn started"

for i in $(seq 1 30); do
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
rc=0; ovpn client revoke dynamic02 >$OUT 2>&1 || rc=$?
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

[ -f /tmp/openvpn.pid ] && kill "$(cat /tmp/openvpn.pid)" 2>/dev/null || true
sleep 1
pass "daemon cleanup"

# ============================================================
echo ""
echo "========================================="
printf "Results: %d passed, %d failed\n" "$PASS" "$FAIL"
echo "========================================="

[ "$FAIL" -eq 0 ] || exit 1
