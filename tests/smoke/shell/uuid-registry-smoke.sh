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
# shellcheck source=../../../rootfs/usr/local/lib/openvpn-container/client-ip.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/client-ip.sh"

assert_rejected() {
  if "$@"; then
    printf 'unexpected success: %s\n' "$*" >&2
    exit 1
  fi
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
ovpn_registry_client_name_valid laptop
assert_rejected ovpn_registry_client_name_valid '550e8400-e29b-41d4-a716-446655440000'

mkdir -p "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/data"
cat >"$OVPN_DATA_DIR/meta/client-state.csv" <<'EOF'
# id,name,state
11111111-1111-4111-8111-111111111111,laptop,deleted
22222222-2222-4222-8222-222222222222,laptop,active
33333333-3333-4333-8333-333333333333,phone,revoked
44444444-4444-4444-8444-444444444444,retired,deleted
EOF
ovpn_registry_load_identities
[ "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[laptop]}" = 22222222-2222-4222-8222-222222222222 ]
deleted_id=11111111-1111-4111-8111-111111111111
[ "${OVPN_REGISTRY_STATE_BY_ID[$deleted_id]}" = deleted ]
assert_rejected ovpn_registry_client_is_deleted laptop
assert_rejected ovpn_registry_client_is_deleted phone
ovpn_registry_client_is_deleted retired
[ "$(ovpn_registry_resolve_current laptop)" = '22222222-2222-4222-8222-222222222222,laptop,active' ]
[ "$(ovpn_registry_resolve_current 22222222-2222-4222-8222-222222222222)" = '22222222-2222-4222-8222-222222222222,laptop,active' ]
[ "$(ovpn_registry_resolve_current phone)" = '33333333-3333-4333-8333-333333333333,phone,revoked' ]
assert_rejected ovpn_registry_resolve_current "$deleted_id"
assert_rejected ovpn_registry_resolve_current retired

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

cat >"$TMP_DIR/invalid-client-ip.csv" <<'EOF'
# id,name,ip
550e8400-e29b-11d4-a716-446655440000,laptop,
EOF
assert_rejected ovpn_client_ip_parse_file "$TMP_DIR/invalid-client-ip.csv"

ovpn_registry_initialize_empty
grep -Fqx '# id,name,state' "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx '# id,name,ip' "$OVPN_DATA_DIR/data/client-ip.csv"
cmp "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"

printf 'UUID registry smoke passed\n'
