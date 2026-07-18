#!/usr/bin/env bash

OVPN_CLIENT_RESOLVED_ID=''
OVPN_CLIENT_RESOLVED_NAME=''
OVPN_CLIENT_RESOLVED_STATE=''

ovpn_client_name_or_die() {
  local name="$1"

  if ! ovpn_registry_client_name_valid "$name"; then
    ovpn_die "invalid client name: $name"
  fi
}

ovpn_client_resolve_ref_or_die() {
  local reference="$1"
  local resolved

  if ! ovpn_registry_uuid_valid "$reference" && ! ovpn_registry_client_name_valid "$reference"; then
    ovpn_die "invalid client reference: $reference"
  fi
  resolved="$(ovpn_registry_resolve_current "$reference")" || ovpn_die "client '$reference' does not exist"
  IFS=, read -r OVPN_CLIENT_RESOLVED_ID OVPN_CLIENT_RESOLVED_NAME OVPN_CLIENT_RESOLVED_STATE <<<"$resolved"
}

ovpn_client_status() {
  local wanted="$1"
  local name id state
  while read -r name id state; do
    if [ "$name" = "$wanted" ]; then
      printf '%s\n' "$state"
      return 0
    fi
  done < <(ovpn_client_records)
  return 1
}

ovpn_client_require_active() {
  local name="$1"
  local status
  status="$(ovpn_client_status "$name" || true)"
  if [ "$status" != active ]; then
    if [ -n "$status" ]; then
      ovpn_die "client '$name' is $status"
    fi
    ovpn_die "client '$name' does not exist"
  fi
}

ovpn_client_refuse_duplicate() {
  local name="$1"
  local status
  status="$(ovpn_client_status "$name" || true)"
  if [ -n "$status" ]; then
    ovpn_die "client '$name' already exists with status $status"
  fi
}

ovpn_client_export_inner() {
  local reference="$1"
  local name
  local profile

  ovpn_require_healthy_state
  ovpn_client_resolve_ref_or_die "$reference"
  name="$OVPN_CLIENT_RESOLVED_NAME"
  ovpn_client_require_active "$name"
  profile="$(ovpn_render_client_content "$name")"
  ovpn_write_or_print "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$profile"
  ovpn_write_or_print - "$profile"
}

ovpn_client_export_command() {
  local reference="${1:-}"
  [ -n "$reference" ] || ovpn_die "usage: ovpn client export <client>"
  [ "$#" -eq 1 ] || ovpn_die "usage: ovpn client export <client>"
  ovpn_with_data_lock client ovpn_client_export_inner "$reference"
}
