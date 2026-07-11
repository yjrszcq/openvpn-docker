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
  local state openvpn_bin config_path

  ovpn_auto_init_if_empty
  state="$(ovpn_state_detect)"
  if [ "$state" != HEALTHY ]; then
    ovpn_log "instance state is $state; refusing to start"
    ovpn_missing_required_files | while IFS= read -r file; do
      [ -n "$file" ] || continue
      ovpn_log "missing required file: $file"
    done
    ovpn_exit_for_state "$state"
  fi

  ovpn_compatibility_require_supported
  config_path="$OVPN_DATA_DIR/server/server.conf"
  ovpn_render_server --output "$config_path"
  openvpn_bin="$(ovpn_openvpn_bin)" || ovpn_die "openvpn is required to start"
  ovpn_log "starting OpenVPN with $config_path"
  exec "$openvpn_bin" --config "$config_path"
}
