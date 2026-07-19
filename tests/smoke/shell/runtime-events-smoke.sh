#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
python3 "$ROOT_DIR/tests/smoke/python/runtime-events-smoke.py"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_DATA_DIR="$TMP_DIR/data"
export OVPN_LEASE_DIR="$OVPN_DATA_DIR/cache/client-leases"
export OVPN_ENDPOINT=vpn.example.test
client_id=11111111-1111-4111-8111-111111111111
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" config apply
mkdir -p "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/logs"
printf '%s\n' '# id,name,state' "$client_id,laptop,active" \
  >"$OVPN_DATA_DIR/meta/client-state.csv"

# Concurrent hook invocations must each append one complete JSON record.
for sequence in $(seq 1 20); do
  second="$(printf '%02d' "$sequence")"
  OVPN_EVENT_TIMESTAMP="2026-01-01T00:00:${second}Z" \
    common_name="$client_id" \
    script_type=client-disconnect \
    ifconfig_pool_remote_ip="10.8.0.$sequence" \
    trusted_ip=2001:db8::10 \
    trusted_port=1194 \
    "$OVPN_LIB_DIR/pool-persist-hook.sh" &
done
wait

[ "$(wc -l <"$OVPN_DATA_DIR/logs/events.jsonl")" -eq 20 ]
jq -e -s --arg id "$client_id" '
  length == 20 and
  all(.[]; .event == "client_connection" and
    .operation == "disconnect" and .outcome == "applied" and
    .client_id == $id and .client_name == "laptop" and
    .remote_ip == "2001:db8::10" and .remote_port == "1194")
' "$OVPN_DATA_DIR/logs/events.jsonl" >/dev/null

"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime events -l 1 -j |
  jq -e --arg id "$client_id" '.client_id == $id and .client_name == "laptop"' >/dev/null
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime events -l 1 |
  grep -Fq 'laptop [111111111111]'
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime events -l 1 -t |
  grep -Fq "laptop [$client_id]"
"$ROOT_DIR/rootfs/usr/local/bin/ovpn" runtime events --help |
  grep -Fq 'usage: ovpn runtime events'

printf 'runtime events shell smoke passed\n'
