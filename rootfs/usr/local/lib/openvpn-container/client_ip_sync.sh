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
  local index name old_ip new_ip line
  local -A old_assignments=()
  local -A new_assignments=()

  [ -r "$snapshot" ] || return 1
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    new_assignments["${OVPN_CLIENT_IP_NAMES[index]}"]="${OVPN_CLIENT_IP_VALUES[index]}"
  done
  while IFS= read -r line || [ -n "$line" ]; do
    [ "$line" = '# client,ip' ] && continue
    [ -n "$line" ] || continue
    name="${line%%,*}"
    old_assignments["$name"]="${line#*,}"
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

  OVPN_CLIENT_IP_SYNC_CCD_STAGE="$(mktemp -d "$OVPN_DATA_DIR/.ccd-ipam.stage.XXXXXX")"
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
  OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS="$(mktemp -d "$OVPN_DATA_DIR/.ccd-ipam.previous.XXXXXX")"
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
  local pool_file="$OVPN_POOL_PERSIST_FILE"
  local line name
  local -A release_names=()

  [ "${#OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS[@]}" -gt 0 ] || return 0
  [ -e "$pool_file" ] || return 0
  mkdir -p "$(dirname "$pool_file")"
  OVPN_CLIENT_IP_SYNC_LEASE_STAGE="$(mktemp "$(dirname "$pool_file")/.pool-persist.XXXXXX")"
  umask 077
  for name in "${OVPN_CLIENT_IP_SYNC_LEASE_CLIENTS[@]}"; do
    release_names["$name"]=1
  done
  while IFS= read -r line || [ -n "$line" ]; do
    name="${line%%,*}"
    [ -n "${release_names[$name]+present}" ] && continue
    printf '%s\n' "$line"
  done <"$pool_file" >"$OVPN_CLIENT_IP_SYNC_LEASE_STAGE"
  chmod 600 "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE"
}

ovpn_client_ip_sync_swap_leases() {
  [ -n "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE" ] || return 0
  mv "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE" "$OVPN_POOL_PERSIST_FILE"
  OVPN_CLIENT_IP_SYNC_LEASE_STAGE=''
  OVPN_CLIENT_IP_SYNC_LEASE_SWAPPED=true
}

ovpn_client_ip_clear_dynamic_lease() {
  local client_name="$1"
  local pool_file="$OVPN_POOL_PERSIST_FILE"
  local temporary line name

  [ -e "$pool_file" ] || return 0
  temporary="$(mktemp "$(dirname "$pool_file")/.pool-persist.XXXXXX")"
  umask 077
  while IFS= read -r line || [ -n "$line" ]; do
    name="${line%%,*}"
    [ "$name" = "$client_name" ] && continue
    printf '%s\n' "$line"
  done <"$pool_file" >"$temporary"
  chmod 600 "$temporary"
  mv "$temporary" "$pool_file"
}

ovpn_client_ip_kick_changed_clients() {
  local client_name response
  local socat_bin="${OVPN_SOCAT_BIN:-socat}"

  [ "${#OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS[@]}" -gt 0 ] || return 0
  [ -S "$OVPN_MANAGEMENT_SOCKET" ] || return 0
  command -v "$socat_bin" >/dev/null 2>&1 || {
    ovpn_log 'client-ip: management socket is present but socat is unavailable'
    return 1
  }
  for client_name in "${OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS[@]}"; do
    if ! response="$(printf 'kill %s\nquit\n' "$client_name" | "$socat_bin" - "UNIX-CONNECT:$OVPN_MANAGEMENT_SOCKET" 2>&1)"; then
      ovpn_log 'client-ip: failed to contact the OpenVPN management socket'
      return 1
    fi
    case "$response" in
      *'ERROR: client not found'*) ;;
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
  if [ -e "$OVPN_POOL_PERSIST_FILE" ]; then
    OVPN_CLIENT_IP_SYNC_LEASE_BACKUP="$(mktemp "$OVPN_DATA_DIR/meta/.pool-persist.backup.XXXXXX")"
    cp "$OVPN_POOL_PERSIST_FILE" "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"
    chmod 600 "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"
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
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP" ] || rm -f "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE" ] || rm -f "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE"
}

ovpn_client_ip_apply_rollback() {
  if [ "$OVPN_CLIENT_IP_SYNC_CCD_SWAPPED" = true ]; then
    rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_DIR"
    if [ "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS_EXISTS" = true ]; then
      mv "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS" "$OVPN_CLIENT_IP_SYNC_CCD_DIR"
    fi
  fi
  if [ "$OVPN_CLIENT_IP_SYNC_LEASE_SWAPPED" = true ] && [ "$OVPN_CLIENT_IP_SYNC_LEASE_EXISTED" = true ]; then
    cp "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP" "$OVPN_POOL_PERSIST_FILE.tmp"
    mv "$OVPN_POOL_PERSIST_FILE.tmp" "$OVPN_POOL_PERSIST_FILE"
    chmod 600 "$OVPN_POOL_PERSIST_FILE"
  fi
  [ -z "$OVPN_CLIENT_IP_SYNC_CCD_STAGE" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_STAGE"
  [ -z "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS" ] || rm -rf "$OVPN_CLIENT_IP_SYNC_CCD_PREVIOUS"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE" ] || rm -f "$OVPN_CLIENT_IP_SYNC_LEASE_STAGE"
  [ -z "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP" ] || rm -f "$OVPN_CLIENT_IP_SYNC_LEASE_BACKUP"
}
