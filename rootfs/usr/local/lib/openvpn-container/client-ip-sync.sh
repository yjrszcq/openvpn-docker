#!/usr/bin/env bash

OVPN_CLIENT_IP_SYNC_CCD_DIR=''
OVPN_CLIENT_IP_SYNC_CCD_STAGE=''
OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS=''
OVPN_CLIENT_IP_SYNC_CCD_SWAPPED=false
OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS_EXISTS=false
OVPN_CLIENT_IP_SYNC_LEASE_BACKUP=''
OVPN_CLIENT_IP_SYNC_LEASE_STAGE=''
OVPN_CLIENT_IP_SYNC_LEASE_EXISTED=false
OVPN_CLIENT_IP_SYNC_LEASE_SWAPPED=false
OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS=()
OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS=()

ovpn_client_ip_sync_reset() {
  OVPN_CLIENT_IP_SYNC_CCD_DIR="$OVPN_DATA_DIR/ccd"
  OVPN_CLIENT_IP_SYNC_CCD_STAGE=''
  OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS=''
  OVPN_CLIENT_IP_SYNC_CCD_SWAPPED=false
  OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS_EXISTS=false
  OVPN_CLIENT_IP_SYNC_LEASE_BACKUP=''
  OVPN_CLIENT_IP_SYNC_LEASE_STAGE=''
  OVPN_CLIENT_IP_SYNC_LEASE_EXISTED=false
  OVPN_CLIENT_IP_SYNC_LEASE_SWAPPED=false
  OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS=()
  OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS=()
}

ovpn_client_ip_sync_collect_changes() {
  local snapshot="$1"
  local draft="$2"
  local index id name old_ip new_ip line
  local -A old_assignments=()
  local -A new_assignments=()

  [ -r "$snapshot" ] || return 1
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    new_assignments["${OVPN_CLIENT_IP_NAMES[index]}"]="${OVPN_CLIENT_IP_VALUES[index]}"
  done
  while IFS= read -r line || [ -n "$line" ]; do
    [ "$line" = '# id,name,ip' ] && continue
    [ -n "$line" ] || continue
    IFS=, read -r id name old_ip <<<"$line"
    old_assignments["$name"]="$old_ip"
  done <"$snapshot"
  for name in "${!new_assignments[@]}"; do
    if [ -n "${old_assignments[$name]+present}" ]; then
      old_ip="${old_assignments[$name]}"
    else
      old_ip=''
    fi
    new_ip="${new_assignments[$name]}"
    [ "$old_ip" = "$new_ip" ] && continue
    OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS+=("$name")
    if [ -z "$old_ip" ] || [ -z "$new_ip" ]; then
      OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS+=("$name")
    fi
  done
  for name in "${!old_assignments[@]}"; do
    [ -z "${new_assignments[$name]+present}" ] || continue
    OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS+=("$name")
    if [ -z "${old_assignments[$name]}" ]; then
      OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS+=("$name")
    fi
  done
}

ovpn_client_ip_sync_stage_ccd() {
  local index name ip

  OVPN_CLIENT_IP_SYNC_CCD_STAGE="$(mktemp -d "$OVPN_DATA_DIR/.ccd-ipam.stage.XXXXXX")" || ovpn_die "failed to create CCD stage directory"
  chmod 700 "$OVPN_CLIENT_IP_SYNC_CCD_STAGE"
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    ip="${OVPN_CLIENT_IP_VALUES[index]}"
    [ -n "$ip" ] || continue
    umask 077
    printf 'ifconfig-push %s %s\n' "$ip" "$OVPN_IPAM_NETMASK" >"$OVPN_CLIENT_IP_SYNC_CCD_STAGE/$name"
    chmod 600 "$OVPN_CLIENT_IP_SYNC_CCD_STAGE/$name"
  done
}

ovpn_client_ip_sync_swap_ccd() {
  OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS="$(mktemp -d "$OVPN_DATA_DIR/.ccd-ipam.previous.XXXXXX")" || ovpn_die "failed to create CCD previous directory"
  rmdir "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS"
  if [ -e "$OVPN_CLIENT_IP_SYNC_CCD_DIR" ]; then
    mv "$OVPN_CLIENT_IP_SYNC_CCD_DIR" "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS"
    OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS_EXISTS=true
  fi
  mv "$OVPN_CLIENT_IP_SYNC_CCD_STAGE" "$OVPN_CLIENT_IP_SYNC_CCD_DIR"
  OVPN_CLIENT_IP_SYNC_CCD_STAGE=''
  OVPN_CLIENT_IP_SYNC_CCD_SWAPPED=true
}

ovpn_client_ip_sync_stage_leases() {
  local lease_dir="$OVPN_LEASE_DIR"

  [ "${#OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS[@]}" -gt 0 ] || return 0
  mkdir -p "$lease_dir"
}

ovpn_client_ip_sync_swap_leases() {
  local name

  [ "${#OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS[@]}" -gt 0 ] || return 0
  for name in "${OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS[@]}"; do
    rm -f "$OVPN_LEASE_DIR/$name"
  done
  OVPN_CLIENT_IP_SYNC_LEASE_SWAPPED=true
}

ovpn_client_ip_clear_dynamic_lease() {
  rm -f "$OVPN_LEASE_DIR/$1"
}

ovpn_client_ip_kick_changed_clients() {
  local client_name response

  [ "${#OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS[@]}" -gt 0 ] || return 0
  [ -S "$OVPN_MANAGEMENT_SOCKET" ] || return 0
  for client_name in "${OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS[@]}"; do
    if ! response="$(ovpn_management_socket_request "$OVPN_MANAGEMENT_SOCKET" "kill $client_name")"; then
      ovpn_log 'client-ip: failed to contact the OpenVPN management socket'
      return 1
    fi
    case "$response" in
      *'ERROR: client not found'|*'ERROR: common name '*"' not found"*)
        continue
        ;;
      *ERROR:*)
        ovpn_log 'client-ip: OpenVPN rejected a management disconnect request'
        return 1
        ;;
    esac
  done
}

ovpn_client_ip_sync_maybe_fail() {
  if [ "${OVPN_CLIENT_IP_APPLY_FAIL_AFTER:-}" = "$1" ]; then
    ovpn_die "injected client-ip apply failure after $1"
  fi
}

ovpn_client_ip_apply_begin() {
  ovpn_client_ip_sync_reset
  if [ -d "$OVPN_LEASE_DIR" ]; then
    OVPN_CLIENT_IP_SYNC_LEASE_BACKUP="$(mktemp -d "$OVPN_DATA_DIR/meta/.leases.backup.XXXXXX")" || ovpn_die "failed to create lease backup directory"
    if [ -n "$(ls -A "$OVPN_LEASE_DIR" 2>/dev/null)" ]; then
      cp -a "$OVPN_LEASE_DIR"/* "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"/ || ovpn_die "failed to back up leases"
    fi
    OVPN_CLIENT_IP_SYNC_LEASE_EXISTED=true
  fi
}

ovpn_client_ip_apply_derived() {
  local snapshot draft

  snapshot="$(ovpn_registry_applied_file)"
  draft="$(ovpn_registry_client_ip_file)"
  ovpn_client_ip_sync_collect_changes "$snapshot" "$draft"
  ovpn_client_ip_sync_stage_ccd
  ovpn_client_ip_sync_swap_ccd
  ovpn_client_ip_sync_maybe_fail ccd
  ovpn_client_ip_sync_stage_leases
  ovpn_client_ip_sync_swap_leases
  ovpn_client_ip_sync_maybe_fail leases
  ovpn_client_ip_kick_changed_clients
}

ovpn_client_ip_apply_finalize() {
  [ -z "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS"
  [ -z "$OVPN_CLIENT_IP_SYNC_CCD_STAGE" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_STAGE"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE"
}

ovpn_client_ip_apply_rollback() {
  if [ "$OVPN_CLIENT_IP_SYNC_CCD_SWAPPED" = true ]; then
    rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_DIR"
    if [ "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS_EXISTS" = true ]; then
      mv "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS" "$OVPN_CLIENT_IP_SYNC_CCD_DIR"
    fi
  fi
  if [ "$OVPN_CLIENT_IP_SYNC_LEASE_SWAPPED" = true ] && [ "$OVPN_CLIENT_IP_SYNC_LEASE_EXISTED" = true ]; then
    find "$OVPN_LEASE_DIR" -maxdepth 1 -type f -delete 2>/dev/null || true
    if [ -n "$(ls -A "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP" 2>/dev/null)" ]; then
      cp -a "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"/* "$OVPN_LEASE_DIR"/ || ovpn_die "failed to restore lease backup"
    fi
  fi
  [ -z "$OVPN_CLIENT_IP_SYNC_CCD_STAGE" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_STAGE"
  [ -z "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"
}
