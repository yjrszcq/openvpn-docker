#!/usr/bin/env bash

ovpn_auto_init_if_empty_inner() {
  if [ "$(ovpn_state_detect)" = EMPTY ]; then
    ovpn_log "data directory is EMPTY; initializing a new instance"
    ovpn_init_inner
  fi
}

ovpn_auto_init_if_empty() {
  ovpn_with_data_lock init ovpn_auto_init_if_empty_inner
}

ovpn_start_command() {
  local state openvpn_bin config_path critical_mode

  critical_mode="$(ovpn_critical_mode)"
  ovpn_auto_init_if_empty
  state="$(ovpn_state_detect)"
  if [ "$state" = DEGRADED_REPAIRABLE ] || [ "$state" = DEGRADED_RECOVERABLE ]; then
    ovpn_log "instance state is $state; applying automatic repairs"
    ovpn_repair_command apply
    state="$(ovpn_state_detect)"
  fi
  if [ "$state" != HEALTHY ]; then
    case "$state" in
      CRITICAL|UNRECOVERABLE)
        if [ "$critical_mode" = maintenance ]; then
          ovpn_maintenance_enter "$state"
        fi
        ovpn_log 'recommended: docker compose run --rm openvpn-maintenance state doctor'
        ovpn_log 'recommended: docker compose run --rm openvpn-maintenance repair plan'
        ;;
    esac
    ovpn_log "instance state is $state; refusing to start"
    while IFS= read -r file; do
      [ -n "$file" ] || continue
      ovpn_log "missing required file: $file"
    done < <(ovpn_missing_required_files)
    ovpn_exit_for_state "$state"
  fi
  ovpn_compatibility_require_supported
  ovpn_network_configure
  config_path="$OVPN_DATA_DIR/server/server.conf"
  ovpn_render_server --output "$config_path"
  openvpn_bin="$(ovpn_openvpn_bin)" || ovpn_die "openvpn is required to start"
  mkdir -p "$OVPN_LEASE_DIR"
  find "$OVPN_LEASE_DIR" -maxdepth 1 -type f -delete 2>/dev/null || true
  export OVPN_LEASE_DIR
  export OVPN_DATA_DIR
  ovpn_runtime_write_state HEALTHY running false
  ovpn_log "starting OpenVPN with $config_path"
  exec "$openvpn_bin" --config "$config_path"
}
