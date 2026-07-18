#!/usr/bin/env bash

ovpn_client_name_or_die() {
  local name="$1"
  if ! [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]; then
    ovpn_die "invalid client name: $name"
  fi
}

ovpn_client_status() {
  local wanted="$1"
  local name state
  while read -r name state; do
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
  local name="$1"
  local profile

  ovpn_require_healthy_state
  ovpn_client_require_active "$name"
  profile="$(ovpn_render_client_content "$name")"
  ovpn_write_or_print "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$profile"
  ovpn_write_or_print - "$profile"
}

ovpn_client_export_command() {
  local name="${1:-}"
  [ -n "$name" ] || ovpn_die "usage: ovpn client export <name>"
  ovpn_client_name_or_die "$name"
  ovpn_with_data_lock client ovpn_client_export_inner "$name"
}
