#!/usr/bin/env bash

ovpn_start_command() {
  local state openvpn_bin config_path
  state="$(ovpn_state_detect)"

  case "$state" in
    EMPTY)
      ovpn_die "data directory is EMPTY; run 'ovpn init' before start in this phase"
      ;;
    HEALTHY)
      config_path="$OVPN_DATA_DIR/server/server.conf"
      ovpn_render_server --output "$config_path"
      openvpn_bin="$(ovpn_openvpn_bin)" || ovpn_die "openvpn is required to start"
      ovpn_log "starting OpenVPN with $config_path"
      exec "$openvpn_bin" --config "$config_path"
      ;;
    *)
      ovpn_log "instance state is $state; refusing to start"
      ovpn_missing_required_files | while IFS= read -r file; do
        [ -n "$file" ] || continue
        ovpn_log "missing required file: $file"
      done
      exit 1
      ;;
  esac
}
