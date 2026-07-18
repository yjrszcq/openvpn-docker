#!/usr/bin/env bash
set -euo pipefail

[ "${OVPN_INTERNAL_MIGRATION_RUNNER:-false}" = true ] || {
  printf 'migration runner is an internal maintenance interface\n' >&2
  exit 78
}

LIB_DIR="${OVPN_LIB_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
. "$LIB_DIR/common.sh"
. "$LIB_DIR/schema.sh"
. "$LIB_DIR/ipam.sh"
. "$LIB_DIR/config.sh"
. "$LIB_DIR/registry.sh"
. "$LIB_DIR/client-ip.sh"
. "$LIB_DIR/network.sh"
. "$LIB_DIR/render.sh"
. "$LIB_DIR/render-ipam.sh"
. "$LIB_DIR/client-ip-sync.sh"
. "$LIB_DIR/recovery.sh"
. "$LIB_DIR/state-ipam.sh"
. "$LIB_DIR/pki.sh"
. "$LIB_DIR/compatibility.sh"

mode="${1:-}"
source_schema="${2:-}"
target_schema="${3:-}"
data_dir="${4:-}"
final_data_dir="${5:-$data_dir}"

if ! [[ "$source_schema" =~ ^[1-9][0-9]*$ ]] ||
  ! [[ "$target_schema" =~ ^[1-9][0-9]*$ ]] ||
  [ ! -d "$data_dir" ]; then
  ovpn_die 'invalid internal migration runner arguments'
fi
[ "$target_schema" = "$OVPN_CURRENT_DATA_SCHEMA" ] ||
  ovpn_die "target bundle cannot migrate to schema $target_schema"

case "$source_schema:$target_schema" in
1:3)
  . "$LIB_DIR/migrations/1-to-2.sh"
  . "$LIB_DIR/migrations/2-to-3.sh"
  ;;
2:3)
  . "$LIB_DIR/migrations/2-to-3.sh"
  ;;
3:3) ;;
*) ovpn_die "target bundle has no migration chain from schema $source_schema to $target_schema" ;;
esac

case "$mode" in
apply)
  if [ "$source_schema" = 1 ]; then
    ovpn_migration_1_to_2_apply_staged "$data_dir"
    ovpn_migration_1_to_2_validate_staged "$data_dir"
  fi
  if [ "$source_schema" -lt 3 ]; then
    ovpn_migration_2_to_3_apply_staged "$data_dir" "$final_data_dir"
  fi
  ;;
validate)
  ovpn_migration_2_to_3_validate_staged "$data_dir"
  ;;
*) ovpn_die 'invalid internal migration runner mode' ;;
esac
