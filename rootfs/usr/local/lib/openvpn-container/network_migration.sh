#!/usr/bin/env bash

ovpn_network_migration_management_command() {
  ovpn_management_socket_request "$OVPN_MANAGEMENT_SOCKET" "$1"
}

ovpn_network_migration_management_version() {
  local response

  response="$(ovpn_network_migration_management_command version)" || return 1
  [[ "$response" != *ERROR:* && "$response" == *'OpenVPN Version:'* ]]
}

ovpn_network_migration_signal_reload() {
  local response

  response="$(ovpn_network_migration_management_command 'signal SIGHUP')" || return 1
  [[ "$response" != *ERROR:* && "$response" == *SUCCESS:* ]]
}

ovpn_network_migration_runtime_preflight() {
  if ! ovpn_network_migration_management_version >/dev/null 2>&1; then
    return 1
  fi
  ovpn_healthcheck_command >/dev/null 2>&1
}

ovpn_network_migration_wait_for_healthy() {
  local rollback="$1"
  local timeout="${OVPN_NETWORK_MIGRATION_HEALTH_TIMEOUT_SECONDS:-15}"
  local deadline

  [[ "$timeout" =~ ^[1-9][0-9]*$ ]] || return 1
  deadline=$((SECONDS + timeout))
  while :; do
    if [ "$rollback" = false ] && [ "${OVPN_NETWORK_MIGRATION_FAIL_HEALTH:-false}" = true ]; then
      return 1
    fi
    if ovpn_network_migration_management_version >/dev/null 2>&1 && ovpn_healthcheck_command >/dev/null 2>&1; then
      return 0
    fi
    [ "$SECONDS" -ge "$deadline" ] && return 1
    sleep 1
  done
}

ovpn_network_migration_reload_and_check() {
  local rollback="$1"

  ovpn_network_migration_signal_reload || return 1
  ovpn_network_migration_wait_for_healthy "$rollback"
}

ovpn_network_migration_audit_event() {
  local outcome="$1"
  local audit_file

  audit_file="$(ovpn_registry_audit_file)"
  printf '{"timestamp":"%s","event":"network_migration","outcome":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$outcome" >>"$audit_file"
  chmod 600 "$audit_file"
}


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
  local index old new_val
  local label_width=13 val_width=2
  local -a client_rows=()

  for ((index = 0; index < ${#OVPN_NETWORK_MIGRATION_NAMES[@]}; index++)); do
    old="${OVPN_CLIENT_IP_VALUES[index]:-dynamic}"
    new_val="${OVPN_NETWORK_MIGRATION_VALUES[index]:-dynamic}"
    client_rows+=("${OVPN_NETWORK_MIGRATION_NAMES[index]}"$'\t'"$old"$'\t'"$new_val")
    if ((${#OVPN_NETWORK_MIGRATION_NAMES[index]} + 1 > label_width)); then
      label_width=$((${#OVPN_NETWORK_MIGRATION_NAMES[index]} + 1))
    fi
    if ((${#old} > val_width)); then val_width=${#old}; fi
    if ((${#new_val} > val_width)); then val_width=${#new_val}; fi
  done

  for v in "$OVPN_NETWORK_MIGRATION_OLD_NETWORK" "$OVPN_NETWORK_MIGRATION_TARGET_NETWORK" \
           "$OVPN_NETWORK_MIGRATION_OLD_POOL" "$OVPN_NETWORK_MIGRATION_TARGET_POOL"; do
    if ((${#v} > val_width)); then val_width=${#v}; fi
  done

  printf '%-*s  %-*s  ->  %s\n' "$label_width" 'Network:' "$val_width" "$OVPN_NETWORK_MIGRATION_OLD_NETWORK" "$OVPN_NETWORK_MIGRATION_TARGET_NETWORK"
  printf '%-*s  %-*s  ->  %s\n' "$label_width" 'Dynamic pool:' "$val_width" "$OVPN_NETWORK_MIGRATION_OLD_POOL" "$OVPN_NETWORK_MIGRATION_TARGET_POOL"

  if ((${#client_rows[@]})); then
    printf '\nClient IP migrations (%d):\n' "${#client_rows[@]}"
    for row in "${client_rows[@]}"; do
      IFS=$'\t' read -r name old new_val <<<"$row"
      printf '%-*s  %-*s  ->  %s\n' "$label_width" "${name}:" "$val_width" "$old" "$new_val"
    done
  fi
}

ovpn_network_migration_apply_inner() (
  local backup config_backup schema_backup draft applied audit server pool index
  local server_existed=false pool_existed=false ccd_existed=false
  local transaction_success=false rollback_ready=false runtime_reload_attempted=false

  ovpn_network_migration_runtime_preflight || ovpn_die 'network migration requires a healthy running OpenVPN process and management socket'

  backup="$(mktemp -d "$OVPN_DATA_DIR/.network-migration.XXXXXX")"
  config_backup="$backup/project.env"
  schema_backup="$backup/schema-version"
  draft="$(ovpn_registry_client_ip_file)"
  applied="$(ovpn_registry_applied_file)"
  audit="$(ovpn_registry_audit_file)"
  server="$OVPN_DATA_DIR/server/server.conf"
  pool="$OVPN_POOL_PERSIST_FILE"

  rollback() {
    cp "$config_backup" "$OVPN_PROJECT_ENV"
    cp "$schema_backup" "$OVPN_SCHEMA_VERSION_FILE"
    cp "$backup/draft" "$draft"
    cp "$backup/applied" "$applied"
    cp "$backup/audit" "$audit"
    rm -rf "$OVPN_DATA_DIR/ccd"
    if [ "$ccd_existed" = true ]; then
      cp -a "$backup/ccd" "$OVPN_DATA_DIR/ccd"
    fi
    if [ "$server_existed" = true ]; then
      cp "$backup/server" "$server"
    else
      rm -f "$server"
    fi
    mkdir -p "$(dirname "$pool")"
    if [ "$pool_existed" = true ]; then
      cp "$backup/pool" "$pool"
    else
      rm -f "$pool"
    fi
  }

  cleanup() {
    local status=$?

    trap - EXIT
    set +e
    if [ "$transaction_success" != true ] && [ "$rollback_ready" = true ]; then
      rollback
      if [ "$runtime_reload_attempted" = true ]; then
        if ovpn_network_migration_reload_and_check true; then
          ovpn_log 'network migration health check failed; rollback completed'
        else
          ovpn_log 'network migration rollback restored persisted state but could not confirm OpenVPN health'
        fi
      else
        ovpn_log 'network migration rollback completed'
      fi
      ovpn_network_migration_audit_event rejected || true
    fi
    rm -rf "$backup"
    exit "$status"
  }
  trap cleanup EXIT

  cp "$OVPN_PROJECT_ENV" "$config_backup"
  cp "$OVPN_SCHEMA_VERSION_FILE" "$schema_backup"
  cp "$draft" "$backup/draft"
  cp "$applied" "$backup/applied"
  cp "$audit" "$backup/audit"
  if [ -e "$OVPN_DATA_DIR/ccd" ]; then
    cp -a "$OVPN_DATA_DIR/ccd" "$backup/ccd"
    ccd_existed=true
  fi
  if [ -e "$server" ]; then
    cp "$server" "$backup/server"
    server_existed=true
  fi
  if [ -e "$pool" ]; then
    cp "$pool" "$backup/pool"
    pool_existed=true
  fi
  rollback_ready=true

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
  ovpn_client_ip_apply_current_mutation
  mkdir -p "$(dirname "$pool")"
  : >"$pool"
  chmod 600 "$pool"
  ovpn_render_server --output "$server"
  runtime_reload_attempted=true
  ovpn_network_migration_reload_and_check false || ovpn_die 'network migration health check failed'
  ovpn_network_migration_audit_event applied

  transaction_success=true
  rm -rf "$backup"
  trap - EXIT
  ovpn_log 'network migration applied'
)


ovpn_network_command() {
  local operation="${1:-}"
  local target_network="" target_pool="" dry_run=false yes=false

  if ovpn_help_requested "$@"; then
    ovpn_network_usage
    return 0
  fi
  [ -n "$operation" ] || ovpn_die "usage: ovpn network <plan|apply> [--network CIDR] [--dynamic-pool-size N] [--yes]"
  shift
  case "$operation" in
    plan)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn network plan [--network <CIDR>] [--dynamic-pool-size <N>]" "Preview a tunnel-network migration without changing state."
        return 0
      fi
      dry_run=true
      ;;
    apply)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn network apply [--network <CIDR>] [--dynamic-pool-size <N>] [--yes]" "Apply a tunnel-network migration after confirmation."
        return 0
      fi
      ;;
    *) ovpn_die "usage: ovpn network <plan|apply> [--network CIDR] [--dynamic-pool-size N] [--yes]" ;;
  esac

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --network) shift; [ "$#" -gt 0 ] || ovpn_die "--network requires CIDR"; target_network="$1" ;;
      --dynamic-pool-size) shift; [ "$#" -gt 0 ] || ovpn_die "--dynamic-pool-size requires N"; target_pool="$1" ;;
      --yes)
        [ "$operation" = apply ] || ovpn_die "usage: ovpn network plan [--network CIDR] [--dynamic-pool-size N]"
        yes=true
        ;;
      *) ovpn_die "usage: ovpn network <plan|apply> [--network CIDR] [--dynamic-pool-size N] [--yes]" ;;
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
