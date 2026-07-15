#!/usr/bin/env bash

ovpn_client_name_or_die() {
  local name="$1"
  if ! [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]; then
    ovpn_die "invalid client name: $name"
  fi
}

ovpn_client_records() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name state
  [ -r "$index" ] || return 0

  while IFS= read -r line || [ -n "$line" ]; do
    [ -n "$line" ] || continue
    status="${line%%$'\t'*}"
    subject="${line##*$'\t'}"
    case "$status" in
      V) state=active ;;
      R) state=revoked ;;
      *) continue ;;
    esac
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ -n "$name" ] || continue
    [ "$name" != "$OVPN_SERVER_NAME" ] || continue
    printf '%s %s\n' "$name" "$state"
  done <"$index"
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
  if [ -e "$OVPN_DATA_DIR/pki/issued/$name.crt" ] || [ -e "$OVPN_DATA_DIR/pki/private/$name.key" ]; then
    ovpn_die "client '$name' already has PKI material"
  fi
}

ovpn_add_client_inner() {
  local name="${1:-}"
  [ -n "$name" ] || ovpn_die "usage: ovpn client create <name>"
  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_refuse_duplicate "$name"

  ovpn_pki_issue_client "$name"
  ovpn_render_client "$name" --output "$OVPN_DATA_DIR/clients/active/$name.ovpn"
  ovpn_log "added client '$name'"
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

ovpn_list_clients_command() {
  ovpn_require_healthy_state
  ovpn_client_records
}

ovpn_revoke_client_inner() {
  local name="${1:-}"
  [ -n "$name" ] || ovpn_die "usage: ovpn client revoke <name>"
  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_require_active "$name"

  ovpn_pki_revoke_client "$name"
  mkdir -p "$OVPN_DATA_DIR/clients/revoked"
  if [ -e "$OVPN_DATA_DIR/clients/active/$name.ovpn" ]; then
    mv "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$OVPN_DATA_DIR/clients/revoked/$name.ovpn"
  fi
  ovpn_log "revoked client '$name'"
}

ovpn_add_client_command() {
  local name="${1:-}"
  [ -n "$name" ] || ovpn_die "usage: ovpn client create <name>"
  ovpn_client_name_or_die "$name"
  ovpn_with_data_lock client ovpn_add_client_inner "$name"
}

ovpn_revoke_client_command() {
  local name="${1:-}"
  [ -n "$name" ] || ovpn_die "usage: ovpn client revoke <name>"
  ovpn_client_name_or_die "$name"
  ovpn_with_data_lock client ovpn_revoke_client_inner "$name"
}
