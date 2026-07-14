#!/usr/bin/env bash

ovpn_state_scan_ipam_consistency() {
  local draft applied state_file audit_file ccd_dir pool_file
  local index name ip expected actual lease_line lease_name
  local -A static_clients=()

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
  if ! (ovpn_client_ip_validate_file "$applied") >/dev/null 2>&1; then
    ovpn_state_add_critical_issue CLIENT_IP_APPLIED_INVALID RESTORE_CLIENT_IP_REGISTRY
    return 0
  fi
  ovpn_config_load
  ovpn_client_ip_parse_file "$applied" || {
    ovpn_state_add_critical_issue CLIENT_IP_APPLIED_INVALID RESTORE_CLIENT_IP_REGISTRY
    return 0
  }
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    ip="${OVPN_CLIENT_IP_VALUES[index]}"
    if [ -z "$ip" ]; then
      if [ -e "$ccd_dir/$name" ]; then
        ovpn_state_add_issue "DYNAMIC_CLIENT_CCD_$name" manual RUN_CLIENT_IP_APPLY
      fi
      continue
    fi
    static_clients["$name"]=1
    expected="ifconfig-push $ip $OVPN_IPAM_NETMASK"
    actual=''
    [ -r "$ccd_dir/$name" ] && actual="$(cat "$ccd_dir/$name")"
    if [ "$actual" != "$expected" ]; then
      ovpn_state_add_issue "STATIC_CLIENT_CCD_$name" manual RUN_CLIENT_IP_APPLY
    fi
  done
  if [ -d "$ccd_dir" ]; then
    shopt -s nullglob
    for lease_line in "$ccd_dir"/*; do
      name="${lease_line##*/}"
      [ -n "${static_clients[$name]+present}" ] || ovpn_state_add_issue "UNEXPECTED_CCD_$name" manual RUN_CLIENT_IP_APPLY
    done
  fi
  if [ -r "$pool_file" ]; then
    while IFS= read -r lease_line || [ -n "$lease_line" ]; do
      lease_name="${lease_line%%,*}"
      [ -n "${static_clients[$lease_name]+present}" ] || continue
      ovpn_state_add_issue "STATIC_CLIENT_LEASE_$lease_name" manual RUN_CLIENT_IP_APPLY
    done <"$pool_file"
  fi
}
