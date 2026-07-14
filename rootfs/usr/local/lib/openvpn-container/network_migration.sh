#!/usr/bin/env bash

ovpn_network_migration_plan() {
  local target_network="$1"
  local target_pool="$2"
  local index name ip value candidate host_mask
  local -A used=()
  local -A planned=()

  ovpn_client_ip_prepare_mutation
  OVPN_NETWORK="$target_network"
  OVPN_DYNAMIC_POOL_SIZE="$target_pool"
  ovpn_config_validate
  target_network="$OVPN_NETWORK"
  target_pool="$OVPN_DYNAMIC_POOL_SIZE"
  OVPN_NETWORK_MIGRATION_TARGET_NETWORK="$target_network"
  OVPN_NETWORK_MIGRATION_TARGET_POOL="$target_pool"
  OVPN_NETWORK_MIGRATION_NAMES=("${OVPN_CLIENT_IP_NAMES[@]}")
  OVPN_NETWORK_MIGRATION_VALUES=("${OVPN_CLIENT_IP_VALUES[@]}")
  host_mask=$(( (1 << (32 - OVPN_IPAM_PREFIX)) - 1 ))
  for ((index = 0; index < ${#OVPN_NETWORK_MIGRATION_NAMES[@]}; index++)); do
    name="${OVPN_NETWORK_MIGRATION_NAMES[index]}"
    ip="${OVPN_NETWORK_MIGRATION_VALUES[index]}"
    [ -n "$ip" ] || continue
    if ovpn_ipam_ip_in_static_range "$ip" && [ -z "${used[$ip]+present}" ]; then
      planned["$name"]="$ip"
      used["$ip"]=1
      continue
    fi
    value="$(ovpn_ipam_ipv4_to_int "$ip")"
    candidate="$(ovpn_ipam_int_to_ipv4 "$((OVPN_IPAM_NETWORK_INT | (value & host_mask)))")"
    if ovpn_ipam_ip_in_static_range "$candidate" && [ -z "${used[$candidate]+present}" ]; then
      planned["$name"]="$candidate"
      used["$candidate"]=1
    fi
  done
  for ((index = 0; index < ${#OVPN_NETWORK_MIGRATION_NAMES[@]}; index++)); do
    name="${OVPN_NETWORK_MIGRATION_NAMES[index]}"
    ip="${OVPN_NETWORK_MIGRATION_VALUES[index]}"
    [ -n "$ip" ] || continue
    [ -n "${planned[$name]+present}" ] && continue
    for ((value = OVPN_IPAM_STATIC_START_INT; value <= OVPN_IPAM_STATIC_END_INT; value++)); do
      candidate="$(ovpn_ipam_int_to_ipv4 "$value")"
      [ -z "${used[$candidate]+present}" ] || continue
      planned["$name"]="$candidate"
      used["$candidate"]=1
      break
    done
    [ -n "${planned[$name]+present}" ] || ovpn_die 'target static address region cannot accommodate existing static clients'
  done
  for ((index = 0; index < ${#OVPN_NETWORK_MIGRATION_NAMES[@]}; index++)); do
    name="${OVPN_NETWORK_MIGRATION_NAMES[index]}"
    ip="${OVPN_NETWORK_MIGRATION_VALUES[index]}"
    [ -n "$ip" ] || {
      [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ] || ovpn_die 'target layout has dynamic clients but dynamic pool capacity is 0'
      continue
    }
    OVPN_NETWORK_MIGRATION_VALUES[index]="${planned[$name]}"
  done
}

ovpn_network_migration_print_plan() {
  local index old

  printf 'Network: %s -> %s\n' "$OVPN_NETWORK_MIGRATION_OLD_NETWORK" "$OVPN_NETWORK_MIGRATION_TARGET_NETWORK"
  printf 'Dynamic pool: %s -> %s\n' "$OVPN_NETWORK_MIGRATION_OLD_POOL" "$OVPN_NETWORK_MIGRATION_TARGET_POOL"
  for ((index = 0; index < ${#OVPN_NETWORK_MIGRATION_NAMES[@]}; index++)); do
    old="${OVPN_CLIENT_IP_VALUES[index]}"
    printf '%s: %s -> %s\n' "${OVPN_NETWORK_MIGRATION_NAMES[index]}" "${old:-dynamic}" "${OVPN_NETWORK_MIGRATION_VALUES[index]:-dynamic}"
  done
}

ovpn_network_migration_apply_inner() {
  local backup config_backup schema_backup draft applied server pool index

  backup="$(mktemp -d "$OVPN_DATA_DIR/.network-migration.XXXXXX")"
  config_backup="$backup/project.env"
  schema_backup="$backup/schema-version"
  draft="$(ovpn_registry_client_ip_file)"
  applied="$(ovpn_registry_applied_file)"
  server="$OVPN_DATA_DIR/server/server.conf"
  pool="$OVPN_POOL_PERSIST_FILE"
  cp "$OVPN_PROJECT_ENV" "$config_backup"
  cp "$OVPN_SCHEMA_VERSION_FILE" "$schema_backup"
  cp "$draft" "$backup/draft"
  cp "$applied" "$backup/applied"
  cp -a "$OVPN_DATA_DIR/ccd" "$backup/ccd"
  [ -e "$server" ] && cp "$server" "$backup/server"
  [ -e "$pool" ] && cp "$pool" "$backup/pool"
  rollback() {
    cp "$config_backup" "$OVPN_PROJECT_ENV"
    cp "$schema_backup" "$OVPN_SCHEMA_VERSION_FILE"
    cp "$backup/draft" "$draft"
    cp "$backup/applied" "$applied"
    rm -rf "$OVPN_DATA_DIR/ccd"
    cp -a "$backup/ccd" "$OVPN_DATA_DIR/ccd"
    [ ! -e "$backup/server" ] || cp "$backup/server" "$server"
    if [ -e "$backup/pool" ]; then cp "$backup/pool" "$pool"; else rm -f "$pool"; fi
    rm -rf "$backup"
  }
  OVPN_NETWORK="$OVPN_NETWORK_MIGRATION_TARGET_NETWORK"
  OVPN_DYNAMIC_POOL_SIZE="$OVPN_NETWORK_MIGRATION_TARGET_POOL"
  OVPN_CONFIG_VERSION=2
  ovpn_config_validate
  ovpn_config_write_loaded
  OVPN_CLIENT_IP_NAMES=("${OVPN_NETWORK_MIGRATION_NAMES[@]}")
  OVPN_CLIENT_IP_VALUES=("${OVPN_NETWORK_MIGRATION_VALUES[@]}")
  OVPN_CLIENT_IP_INTS=()
  for ((index = 0; index < ${#OVPN_CLIENT_IP_VALUES[@]}; index++)); do
    if [ -n "${OVPN_CLIENT_IP_VALUES[index]}" ]; then OVPN_CLIENT_IP_INTS+=("$(ovpn_ipam_ipv4_to_int "${OVPN_CLIENT_IP_VALUES[index]}")"); else OVPN_CLIENT_IP_INTS+=(''); fi
  done
  if ! ovpn_client_ip_apply_current_mutation; then rollback; ovpn_die 'network migration failed while applying client assignments'; fi
  mkdir -p "$(dirname "$pool")"
  : >"$pool"
  chmod 600 "$pool"
  if ! ovpn_render_server --output "$server"; then rollback; ovpn_die 'network migration failed while rendering server configuration'; fi
  if [ "${OVPN_NETWORK_MIGRATION_FAIL_HEALTH:-false}" = true ]; then rollback; ovpn_die 'network migration health check failed; rollback completed'; fi
  rm -rf "$backup"
  ovpn_log 'network migration applied'
}

ovpn_network_reconfigure_command() {
  local target_network='' target_pool='' dry_run=false yes=false

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --network) shift; [ "$#" -gt 0 ] || ovpn_die '--network requires CIDR'; target_network="$1" ;;
      --dynamic-pool-size) shift; [ "$#" -gt 0 ] || ovpn_die '--dynamic-pool-size requires N'; target_pool="$1" ;;
      --dry-run) dry_run=true ;;
      --yes) yes=true ;;
      *) ovpn_die 'usage: ovpn network reconfigure [--network CIDR] [--dynamic-pool-size N] [--dry-run] [--yes]' ;;
    esac
    shift
  done
  ovpn_with_data_lock client ovpn_network_reconfigure_locked "$target_network" "$target_pool" "$dry_run" "$yes"
}

ovpn_network_reconfigure_locked() {
  local target_network="$1" target_pool="$2" dry_run="$3" yes="$4" answer

  ovpn_config_load
  OVPN_NETWORK_MIGRATION_OLD_NETWORK="$OVPN_NETWORK"
  OVPN_NETWORK_MIGRATION_OLD_POOL="$OVPN_DYNAMIC_POOL_SIZE"
  target_network="${target_network:-$OVPN_NETWORK}"
  target_pool="${target_pool:-$OVPN_DYNAMIC_POOL_SIZE}"
  ovpn_network_migration_plan "$target_network" "$target_pool"
  ovpn_network_migration_print_plan
  [ "$dry_run" = true ] && return 0
  if [ "$yes" != true ]; then
    [ -r /dev/tty ] || ovpn_die 'network migration requires --yes when no interactive terminal is available'
    read -r -p 'Apply this network migration? [y/N] ' answer </dev/tty
    [ "$answer" = y ] || [ "$answer" = Y ] || return 0
  fi
  ovpn_network_migration_apply_inner
}
