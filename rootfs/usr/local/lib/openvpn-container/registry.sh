#!/usr/bin/env bash

ovpn_registry_dir() {
  printf '%s/data\n' "$OVPN_DATA_DIR"
}

ovpn_registry_client_ip_file() {
  printf '%s/client-ip.csv\n' "$(ovpn_registry_dir)"
}

ovpn_registry_applied_file() {
  printf '%s/meta/client-ip.applied.csv\n' "$OVPN_DATA_DIR"
}

ovpn_registry_client_state_file() {
  printf '%s/meta/client-state.csv\n' "$OVPN_DATA_DIR"
}

ovpn_registry_audit_file() {
  printf '%s/meta/audit.jsonl\n' "$OVPN_DATA_DIR"
}

ovpn_registry_write_empty() {
  local client_ip_file="$1"
  local state_file="$2"
  local audit_file="$3"

  mkdir -p "$(dirname "$client_ip_file")" "$(dirname "$state_file")"
  umask 077
  printf '%s\n' '# client,ip' >"$client_ip_file.tmp"
  mv "$client_ip_file.tmp" "$client_ip_file"
  printf '%s\n' '# client,state' >"$state_file.tmp"
  mv "$state_file.tmp" "$state_file"
  : >"$audit_file"
  chmod 600 "$client_ip_file" "$state_file" "$audit_file"
}

ovpn_registry_initialize_empty() {
  local client_ip_file state_file audit_file applied_file

  client_ip_file="$(ovpn_registry_client_ip_file)"
  state_file="$(ovpn_registry_client_state_file)"
  audit_file="$(ovpn_registry_audit_file)"
  applied_file="$(ovpn_registry_applied_file)"
  ovpn_registry_write_empty "$client_ip_file" "$state_file" "$audit_file"
  cp "$client_ip_file" "$applied_file.tmp"
  mv "$applied_file.tmp" "$applied_file"
  chmod 600 "$applied_file"
}

ovpn_registry_files_ready() {
  [ -r "$(ovpn_registry_client_ip_file)" ] && \
    [ -r "$(ovpn_registry_applied_file)" ] && \
    [ -r "$(ovpn_registry_client_state_file)" ] && \
    [ -r "$(ovpn_registry_audit_file)" ]
}

ovpn_registry_client_name_valid() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]
}

ovpn_registry_client_is_deleted() {
  local wanted="$1"
  local state_file line name state

  state_file="$(ovpn_registry_client_state_file)"
  [ -r "$state_file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    [ "$line" = '# client,state' ] && continue
    name="${line%%,*}"
    state="${line#*,}"
    [ "$name" = "$wanted" ] && [ "$state" = deleted ] && return 0
  done <"$state_file"
  return 1
}
