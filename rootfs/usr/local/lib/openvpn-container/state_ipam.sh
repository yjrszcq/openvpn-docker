#!/usr/bin/env bash

declare -A OVPN_STATE_IPAM_CLIENT_STATES=()

ovpn_state_ipam_parse_client_states() {
  local state_file="$1"
  local line line_number=0 name state header_seen=false

  OVPN_STATE_IPAM_CLIENT_STATES=()
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [ "$line" = '# client,state' ]; then
      if [ "$header_seen" = true ] || [ "${#OVPN_STATE_IPAM_CLIENT_STATES[@]}" -ne 0 ]; then
        return 1
      fi
      header_seen=true
      continue
    fi
    [ "$header_seen" = true ] || return 1
    [[ "$line" != *[[:space:]]* ]] || return 1
    [[ "$line" = *,* && "$line" != *,*,* ]] || return 1
    name="${line%%,*}"
    state="${line#*,}"
    ovpn_registry_client_name_valid "$name" || return 1
    case "$state" in active|revoked|deleted) ;; *) return 1 ;; esac
    [ -z "${OVPN_STATE_IPAM_CLIENT_STATES[$name]+present}" ] || return 1
    OVPN_STATE_IPAM_CLIENT_STATES["$name"]="$state"
  done <"$state_file"
  [ "$header_seen" = true ]
}

ovpn_state_ipam_deleted_client_has_active_certificate() {
  local wanted="$1"
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name

  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    [ "$status" = V ] || continue
    subject="${line##*$'\t'}"
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ "$name" = "$wanted" ] && return 0
  done <"$index"
  return 1
}

ovpn_state_ipam_audit_is_valid() {
  local audit_file="$1"
  local line timestamp regex
  timestamp='[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z'

  while IFS= read -r line || [ -n "$line" ]; do
    regex="^\\{\"timestamp\":\"${timestamp}\",\"event\":\"client_ip_apply\",\"outcome\":\"(applied|rejected)\"\\}$"
    [[ "$line" =~ $regex ]] && continue
    regex="^\\{\"timestamp\":\"${timestamp}\",\"operation\":\"(revoke|reissue|delete)\",\"result\":\"(applied|rejected|failed)\"\\}$"
    [[ "$line" =~ $regex ]] && continue
    return 1
  done <"$audit_file"
}

ovpn_state_ipam_private_mode() {
  local file="$1"
  local mode

  mode="$(stat -c '%a' "$file" 2>/dev/null)" || return 1
  [ "$mode" = 600 ]
}

ovpn_state_ipam_applied_is_canonical() {
  local applied="$1"
  local canonical

  canonical="$(mktemp "${TMPDIR:-/tmp}/ovpn-client-ip-canonical.XXXXXX")"
  if ! ovpn_client_ip_write_canonical_file "$canonical" || ! cmp -s "$applied" "$canonical"; then
    rm -f "$canonical"
    return 1
  fi
  rm -f "$canonical"
}

ovpn_state_ipam_stage_ccd() {
  local destination="$1"
  local applied index name ip

  applied="$(ovpn_registry_applied_file)"
  ovpn_client_ip_validate_file "$applied" || ovpn_die 'cannot safely rebuild CCD from an invalid applied client-IP registry'
  rm -rf "$destination"
  mkdir -p "$destination"
  chmod 700 "$destination"
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    ip="${OVPN_CLIENT_IP_VALUES[index]}"
    [ -n "$ip" ] || continue
    printf 'ifconfig-push %s %s\n' "$ip" "$OVPN_IPAM_NETMASK" >"$destination/$name"
    chmod 600 "$destination/$name"
  done
}

ovpn_state_ipam_stage_canonical_registry() {
  local destination="$1"
  local applied

  applied="$(ovpn_registry_applied_file)"
  ovpn_client_ip_validate_file "$applied" || ovpn_die 'cannot safely normalize an invalid applied client-IP registry'
  mkdir -p "$(dirname "$destination")"
  ovpn_client_ip_write_canonical_file "$destination"
}

ovpn_state_scan_ipam_consistency() {
  local draft applied state_file audit_file ccd_dir pool_file protected
  local index name ip expected actual lease_line lease_name state expected_state
  local ccd_out_of_sync=false
  local -A applied_clients=()

  draft="$(ovpn_registry_client_ip_file)"
  applied="$(ovpn_registry_applied_file)"
  state_file="$(ovpn_registry_client_state_file)"
  audit_file="$(ovpn_registry_audit_file)"
  ccd_dir="$OVPN_DATA_DIR/ccd"
  pool_file="$OVPN_POOL_PERSIST_FILE"
  if [ ! -e "$draft" ] && [ ! -e "$applied" ] && [ ! -e "$state_file" ] && [ ! -e "$audit_file" ]; then
    return 0
  fi
  if [ ! -r "$draft" ] || [ ! -r "$applied" ] || [ ! -r "$state_file" ] || [ ! -r "$audit_file" ]; then
    ovpn_state_add_critical_issue CLIENT_IP_REGISTRY_INCOMPLETE RESTORE_CLIENT_IP_REGISTRY
    return 0
  fi
  for protected in "$draft" "$applied" "$state_file" "$audit_file"; do
    if ! ovpn_state_ipam_private_mode "$protected"; then
      ovpn_state_add_critical_issue CLIENT_IP_REGISTRY_PERMISSIONS RESTORE_CLIENT_IP_REGISTRY
      return 0
    fi
  done
  if ! ovpn_state_ipam_parse_client_states "$state_file"; then
    ovpn_state_add_critical_issue CLIENT_IP_STATE_INVALID RESTORE_CLIENT_IP_REGISTRY
    return 0
  fi
  if ! ovpn_state_ipam_audit_is_valid "$audit_file"; then
    ovpn_state_add_critical_issue CLIENT_IP_AUDIT_INVALID RESTORE_AUDIT_LOG
    return 0
  fi
  if ! (ovpn_client_ip_validate_file "$applied") >/dev/null 2>&1; then
    ovpn_state_add_critical_issue CLIENT_IP_APPLIED_INVALID RESTORE_CLIENT_IP_REGISTRY
    return 0
  fi
  ovpn_client_ip_validate_file "$applied" || {
    ovpn_state_add_critical_issue CLIENT_IP_APPLIED_INVALID RESTORE_CLIENT_IP_REGISTRY
    return 0
  }
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    ip="${OVPN_CLIENT_IP_VALUES[index]}"
    applied_clients["$name"]=1
    state="${OVPN_STATE_IPAM_CLIENT_STATES[$name]:-}"
    expected_state="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
    if [ "$state" != "$expected_state" ]; then
      ovpn_state_add_critical_issue CLIENT_IP_LOGICAL_STATE_MISMATCH RESTORE_CLIENT_IP_REGISTRY
      return 0
    fi
    if [ -z "$ip" ]; then
      if [ -e "$ccd_dir/$name" ]; then ccd_out_of_sync=true; fi
      continue
    fi
    expected="ifconfig-push $ip $OVPN_IPAM_NETMASK"
    actual=''
    [ -r "$ccd_dir/$name" ] && actual="$(cat "$ccd_dir/$name")"
    if [ "$actual" != "$expected" ]; then ccd_out_of_sync=true; fi
  done
  for name in "${!OVPN_CLIENT_IP_PKI_STATES[@]}"; do
    [ "${OVPN_STATE_IPAM_CLIENT_STATES[$name]:-}" = "${OVPN_CLIENT_IP_PKI_STATES[$name]}" ] || {
      ovpn_state_add_critical_issue CLIENT_IP_LOGICAL_STATE_MISMATCH RESTORE_CLIENT_IP_REGISTRY
      return 0
    }
  done
  for name in "${!OVPN_STATE_IPAM_CLIENT_STATES[@]}"; do
    state="${OVPN_STATE_IPAM_CLIENT_STATES[$name]}"
    if [ "$state" = deleted ]; then
      if [ -n "${applied_clients[$name]+present}" ] || ovpn_state_ipam_deleted_client_has_active_certificate "$name"; then
        ovpn_state_add_critical_issue CLIENT_IP_LOGICAL_STATE_MISMATCH RESTORE_CLIENT_IP_REGISTRY
        return 0
      fi
    elif [ -z "${applied_clients[$name]+present}" ]; then
      ovpn_state_add_critical_issue CLIENT_IP_LOGICAL_STATE_MISMATCH RESTORE_CLIENT_IP_REGISTRY
      return 0
    fi
  done
  if [ -d "$ccd_dir" ]; then
    shopt -s nullglob
    for lease_line in "$ccd_dir"/*; do
      name="${lease_line##*/}"
      [ -n "${applied_clients[$name]+present}" ] && [ -n "$(awk -F, -v client="$name" '$1 == client && $2 != "" { print; exit }' "$applied")" ] || ccd_out_of_sync=true
    done
  fi
  if [ "$ccd_out_of_sync" = true ]; then
    ovpn_state_add_repairable_issue CLIENT_IP_CCD_OUT_OF_SYNC SYNCHRONIZE_CLIENT_IP_CCD
  fi
  if [ -r "$pool_file" ]; then
    while IFS= read -r lease_line || [ -n "$lease_line" ]; do
      lease_name="${lease_line%%,*}"
      [ -n "${applied_clients[$lease_name]+present}" ] || continue
      ip="$(awk -F, -v client="$lease_name" '$1 == client { print $2; exit }' "$applied")"
      [ -z "$ip" ] || ovpn_state_add_issue "STATIC_CLIENT_LEASE_$lease_name" manual RUN_CLIENT_IP_APPLY
    done <"$pool_file"
  fi
  if cmp -s "$draft" "$applied" && ! ovpn_state_ipam_applied_is_canonical "$applied"; then
    ovpn_state_add_repairable_issue CLIENT_IP_REGISTRY_NOT_CANONICAL NORMALIZE_CLIENT_IP_REGISTRY
  fi
}
