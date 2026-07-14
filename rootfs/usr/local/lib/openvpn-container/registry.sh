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

ovpn_registry_write_v1_clients() {
  local client_ip_file="$1"
  local state_file="$2"
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name
  local -A states=()

  [ -r "$index" ] || ovpn_die "cannot migrate V1 registry without PKI index: $index"
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in
      V|R) ;;
      *) continue ;;
    esac
    subject="${line##*$'\t'}"
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ "$name" = "$OVPN_SERVER_NAME" ] && continue
    ovpn_registry_client_name_valid "$name" || ovpn_die "invalid client name in PKI index: $name"
    if [ "$status" = V ]; then
      states["$name"]=active
    elif [ -z "${states[$name]:-}" ]; then
      states["$name"]=revoked
    fi
  done <"$index"

  umask 077
  {
    printf '%s\n' '# client,ip'
    for name in "${!states[@]}"; do
      printf '%s,\n' "$name"
    done | LC_ALL=C sort
  } >"$client_ip_file.tmp"
  {
    printf '%s\n' '# client,state'
    for name in "${!states[@]}"; do
      printf '%s,%s\n' "$name" "${states[$name]}"
    done | LC_ALL=C sort
  } >"$state_file.tmp"
  mv "$client_ip_file.tmp" "$client_ip_file"
  mv "$state_file.tmp" "$state_file"
  chmod 600 "$client_ip_file" "$state_file"
}

ovpn_registry_upgrade_v1_inner() {
  local client_ip_file state_file audit_file applied_file

  ovpn_config_load
  ovpn_registry_files_ready && return 0
  if [ "$OVPN_CONFIG_VERSION" != 1 ]; then
    ovpn_die 'V2 client registry is incomplete; restore it from a backup before starting'
  fi

  client_ip_file="$(ovpn_registry_client_ip_file)"
  state_file="$(ovpn_registry_client_state_file)"
  audit_file="$(ovpn_registry_audit_file)"
  applied_file="$(ovpn_registry_applied_file)"
  mkdir -p "$(dirname "$client_ip_file")" "$(dirname "$state_file")"
  ovpn_registry_write_v1_clients "$client_ip_file" "$state_file"
  : >"$audit_file"
  cp "$client_ip_file" "$applied_file.tmp"
  mv "$applied_file.tmp" "$applied_file"
  chmod 600 "$applied_file" "$audit_file"
  if [ "$OVPN_CONFIG_VERSION" = 1 ]; then
    OVPN_CONFIG_VERSION=2
    ovpn_config_write_loaded
  fi
  ovpn_log 'migrated V1 client state to the V2 dynamic registry'
}

ovpn_registry_upgrade_v1() {
  ovpn_with_data_lock registry ovpn_registry_upgrade_v1_inner
}
