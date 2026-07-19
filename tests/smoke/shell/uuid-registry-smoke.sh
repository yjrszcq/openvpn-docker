#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_DATA_DIR="$TMP_DIR/data"
# shellcheck source=../../../rootfs/usr/local/lib/openvpn-container/common.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/common.sh"
# shellcheck source=../../../rootfs/usr/local/lib/openvpn-container/registry.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/registry.sh"
# shellcheck source=../../../rootfs/usr/local/lib/openvpn-container/client.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/client.sh"
# shellcheck source=../../../rootfs/usr/local/lib/openvpn-container/client-ip.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/client-ip.sh"

assert_rejected() {
  if "$@"; then
    printf 'unexpected success: %s\n' "$*" >&2
    exit 1
  fi
}

assert_status() {
  local expected="$1"
  shift
  local status=0
  "$@" >/dev/null 2>&1 || status=$?
  [ "$status" -eq "$expected" ] || {
    printf 'unexpected status %s (wanted %s): %s\n' "$status" "$expected" "$*" >&2
    exit 1
  }
}

declare -A generated=()
for _ in {1..100}; do
  id="$(ovpn_registry_uuid_generate)"
  ovpn_registry_uuid_valid "$id"
  [ -z "${generated[$id]+present}" ]
  generated["$id"]=1
done

ovpn_registry_uuid_valid '550e8400-e29b-41d4-a716-446655440000'
assert_rejected ovpn_registry_uuid_valid '550e8400-e29b-11d4-a716-446655440000'
assert_rejected ovpn_registry_uuid_valid '550E8400-E29B-41D4-A716-446655440000'
[ "$(ovpn_registry_uuid_compact 550e8400-e29b-41d4-a716-446655440000)" = 550e8400e29b41d4a716446655440000 ]
[ "$(ovpn_registry_uuid_abbreviate 550e8400-e29b-41d4-a716-446655440000)" = 550e8400e29b ]
[ "$(ovpn_registry_uuid_reference_normalize 550E8400-E29B-41D4-A716-446655440000)" = 550e8400e29b41d4a716446655440000 ]
[ "$(ovpn_registry_uuid_reference_normalize 550E8400)" = 550e8400 ]
assert_rejected ovpn_registry_uuid_reference_normalize 550e840
assert_rejected ovpn_registry_uuid_reference_normalize 550e840g
ovpn_registry_client_name_valid laptop
assert_rejected ovpn_registry_client_name_valid '550e8400-e29b-41d4-a716-446655440000'

mkdir -p "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/data"
cat >"$OVPN_DATA_DIR/meta/client-state.csv" <<'EOF'
# id,name,state
11111111-1111-4111-8111-111111111111,laptop,deleted
22222222-2222-4222-8222-222222222222,laptop,active
33333333-3333-4333-8333-333333333333,phone,revoked
44444444-4444-4444-8444-444444444444,retired,deleted
22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa,22222222,revoked
55555555-5555-4555-8555-555555555555,deleted-prefix,deleted
EOF
ovpn_registry_load_identities
[ "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[laptop]}" = 22222222-2222-4222-8222-222222222222 ]
deleted_id=11111111-1111-4111-8111-111111111111
[ "${OVPN_REGISTRY_STATE_BY_ID[$deleted_id]}" = deleted ]
assert_rejected ovpn_registry_client_is_deleted laptop
assert_rejected ovpn_registry_client_is_deleted phone
ovpn_registry_client_is_deleted retired
[ "$(ovpn_registry_resolve_current_by_id 222222222222)" = '22222222-2222-4222-8222-222222222222,laptop,active' ]
[ "$(ovpn_registry_resolve_current_by_id 2222222222224222822222222222222)" = '22222222-2222-4222-8222-222222222222,laptop,active' ]
[ "$(ovpn_registry_resolve_current_by_id 22222222222242228222222222222222)" = '22222222-2222-4222-8222-222222222222,laptop,active' ]
[ "$(ovpn_registry_resolve_current_by_id 22222222-2222-4222-8222-222222222222)" = '22222222-2222-4222-8222-222222222222,laptop,active' ]
[ "$(ovpn_registry_resolve_current_by_id 22222222-AAAA-4AAA-8AAA-AAAAAAAAAAAA)" = '22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa,22222222,revoked' ]
[ "$(ovpn_registry_resolve_current_by_id 33333333)" = '33333333-3333-4333-8333-333333333333,phone,revoked' ]
[ "$(ovpn_registry_resolve_current_by_name 22222222)" = '22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa,22222222,revoked' ]
assert_status "$OVPN_REGISTRY_RESOLVE_AMBIGUOUS" ovpn_registry_resolve_current_by_id 22222222
assert_status "$OVPN_REGISTRY_RESOLVE_INVALID" ovpn_registry_resolve_current_by_id 2222222
assert_status "$OVPN_REGISTRY_RESOLVE_NOT_FOUND" ovpn_registry_resolve_current_by_id aaaaaaaa
assert_status "$OVPN_REGISTRY_RESOLVE_NOT_FOUND" ovpn_registry_resolve_current_by_id 55555555
assert_status "$OVPN_REGISTRY_RESOLVE_NOT_FOUND" ovpn_registry_resolve_current_by_name missing-client
assert_rejected ovpn_registry_resolve_current_by_id "$deleted_id"
assert_rejected ovpn_registry_resolve_current_by_name retired

ovpn_client_parse_single_selector_or_die usage -i 33333333 trailing
[ "$OVPN_CLIENT_SELECTOR_MODE" = id ]
[ "$OVPN_CLIENT_SELECTOR_REFERENCE" = 33333333 ]
[ "$OVPN_CLIENT_SELECTOR_CONSUMED" -eq 2 ]
ovpn_client_resolve_selector_or_die id 33333333
[ "$OVPN_CLIENT_RESOLVED_NAME" = phone ]
ovpn_client_resolve_selector_or_die name 22222222
[ "$OVPN_CLIENT_RESOLVED_ID" = 22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa ]
ovpn_client_parse_single_selector_or_die usage 22222222 trailing
[ "$OVPN_CLIENT_SELECTOR_MODE" = name ]
[ "$OVPN_CLIENT_SELECTOR_REFERENCE" = 22222222 ]
[ "$OVPN_CLIENT_SELECTOR_CONSUMED" -eq 1 ]
ovpn_client_resolve_selector_or_die "$OVPN_CLIENT_SELECTOR_MODE" "$OVPN_CLIENT_SELECTOR_REFERENCE"
[ "$OVPN_CLIENT_RESOLVED_ID" = 22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa ]
if (ovpn_client_resolve_selector_or_die name 33333333) 2>"$TMP_DIR/positional-id.err"; then
  echo 'ID-like positional name unexpectedly selected a UUID prefix' >&2
  exit 1
fi
grep -Fq "client name '33333333' does not exist" "$TMP_DIR/positional-id.err"
if (ovpn_client_resolve_selector_or_die id 2222222) 2>"$TMP_DIR/short-id.err"; then
  echo 'short client ID unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'use 8-32 hexadecimal characters or a full UUID' "$TMP_DIR/short-id.err"

cat >"$TMP_DIR/duplicate-current.csv" <<'EOF'
# id,name,state
11111111-1111-4111-8111-111111111111,laptop,active
22222222-2222-4222-8222-222222222222,laptop,revoked
EOF
assert_rejected ovpn_registry_load_identities "$TMP_DIR/duplicate-current.csv"

cat >"$TMP_DIR/duplicate-id.csv" <<'EOF'
# id,name,state
11111111-1111-4111-8111-111111111111,laptop,deleted
11111111-1111-4111-8111-111111111111,phone,active
EOF
assert_rejected ovpn_registry_load_identities "$TMP_DIR/duplicate-id.csv"

cat >"$TMP_DIR/client-ip.csv" <<'EOF'
# id,name,ip
22222222-2222-4222-8222-222222222222,laptop,
33333333-3333-4333-8333-333333333333,phone,
EOF
ovpn_client_ip_parse_file "$TMP_DIR/client-ip.csv"
[ "${OVPN_CLIENT_IP_IDS[0]}" = 22222222-2222-4222-8222-222222222222 ]
[ "${OVPN_CLIENT_IP_NAMES[1]}" = phone ]

mkdir -p "$OVPN_DATA_DIR/pki"
cat >"$OVPN_DATA_DIR/pki/index.txt" <<'EOF'
V	30000101000000Z		01	unknown	/CN=22222222-2222-4222-8222-222222222222
R	30000101000000Z	260101000000Z	02	unknown	/CN=33333333-3333-4333-8333-333333333333
R	30000101000000Z	260101000000Z	03	unknown	/CN=legacy-revoked-cn
R	30000101000000Z	260101000000Z	04	unknown	/CN=22222222-aaaa-4aaa-8aaa-aaaaaaaaaaaa
EOF
ovpn_client_ip_collect_pki_clients
[ "${OVPN_CLIENT_IP_PKI_STATES[laptop]}" = active ]
[ "${OVPN_CLIENT_IP_PKI_STATES[phone]}" = revoked ]
printf 'V\t30000101000000Z\t\t05\tunknown\t/CN=legacy-active-cn\n' >>"$OVPN_DATA_DIR/pki/index.txt"
assert_rejected ovpn_client_ip_collect_pki_clients
sed -i '$d' "$OVPN_DATA_DIR/pki/index.txt"

cat >"$TMP_DIR/invalid-client-ip.csv" <<'EOF'
# id,name,ip
550e8400-e29b-11d4-a716-446655440000,laptop,
EOF
assert_rejected ovpn_client_ip_parse_file "$TMP_DIR/invalid-client-ip.csv"

ovpn_registry_initialize_empty
grep -Fqx '# id,name,state' "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx '# id,name,ip' "$OVPN_DATA_DIR/meta/client-ip.csv"

printf 'UUID registry smoke passed\n'
