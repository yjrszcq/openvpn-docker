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

ovpn_start_management_broker() {
  local python_bin raw_log broker_pid

  python_bin="${OVPN_PYTHON_BIN:-python3}"
  raw_log="${OVPN_RAW_LOG_FILE:-$OVPN_DATA_DIR/logs/openvpn.log}"
  command -v "$python_bin" >/dev/null 2>&1 ||
    ovpn_die 'python3 is required for the OpenVPN management broker'
  rm -f "$OVPN_MANAGEMENT_SOCKET" "$OVPN_OPENVPN_MANAGEMENT_SOCKET"
  "$python_bin" "$LIB_DIR/management-broker.py" \
    --listen "$OVPN_MANAGEMENT_SOCKET" \
    --backend "$OVPN_OPENVPN_MANAGEMENT_SOCKET" \
    --raw-log "$raw_log" \
    --max-bytes "$OVPN_LOG_MAX_BYTES" \
    --backups "$OVPN_LOG_BACKUPS" \
    --reload-script "${OVPN_MANAGEMENT_BROKER_RELOAD_SCRIPT:-/usr/local/lib/openvpn-management-runtime/current/lib/management-broker.py}" &
  broker_pid=$!
  OVPN_MANAGEMENT_BROKER_PID="$broker_pid"
  printf '%s\n' "$broker_pid" >"$OVPN_RUNTIME_DIR/management-broker.pid"
  chmod 600 "$OVPN_RUNTIME_DIR/management-broker.pid"
  for _ in {1..50}; do
    [ -S "$OVPN_MANAGEMENT_SOCKET" ] && return 0
    kill -0 "$broker_pid" >/dev/null 2>&1 ||
      ovpn_die 'OpenVPN management broker exited during startup'
    sleep 0.1
  done
  kill "$broker_pid" >/dev/null 2>&1 || true
  ovpn_die 'OpenVPN management broker did not create its socket'
}

ovpn_start_supervise() {
  local openvpn_bin="$1"
  local config_path="$2"
  local openvpn_pid status

  "$openvpn_bin" --config "$config_path" &
  openvpn_pid=$!
  trap 'kill -TERM "$openvpn_pid" >/dev/null 2>&1 || true' TERM
  trap 'kill -INT "$openvpn_pid" >/dev/null 2>&1 || true' INT
  trap 'kill -HUP "$openvpn_pid" >/dev/null 2>&1 || true' HUP
  if wait "$openvpn_pid"; then
    status=0
  else
    status=$?
  fi
  trap - TERM INT HUP
  if [ -n "${OVPN_MANAGEMENT_BROKER_PID:-}" ]; then
    kill -TERM "$OVPN_MANAGEMENT_BROKER_PID" >/dev/null 2>&1 || true
    wait "$OVPN_MANAGEMENT_BROKER_PID" 2>/dev/null || true
    rm -f "$OVPN_RUNTIME_DIR/management-broker.pid"
  fi
  return "$status"
}

ovpn_start_inner() {
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
    CRITICAL | UNRECOVERABLE)
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
  mkdir -p "$OVPN_RUNTIME_DIR"
  if [ "${OVPN_MANAGEMENT_BROKER_DISABLED:-false}" = false ]; then
    ovpn_start_management_broker
  else
    [ "$OVPN_MANAGEMENT_BROKER_DISABLED" = true ] ||
      ovpn_die 'OVPN_MANAGEMENT_BROKER_DISABLED must be true or false'
    OVPN_MANAGEMENT_BROKER_PID=''
  fi
  mkdir -p "$OVPN_LEASE_DIR"
  find "$OVPN_LEASE_DIR" -maxdepth 1 -type f -delete 2>/dev/null || true
  export OVPN_LEASE_DIR
  export OVPN_DATA_DIR
  ovpn_runtime_write_state HEALTHY running false
  ovpn_log "starting OpenVPN with $config_path"
  ovpn_start_supervise "$openvpn_bin" "$config_path"
}

ovpn_start_command() {
  ovpn_with_runtime_shared_lock ovpn_start_inner "$@"
}
