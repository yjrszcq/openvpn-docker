#!/usr/bin/env bash

OVPN_RUNTIME_STATE_FILE="${OVPN_RUNTIME_STATE_FILE:-$OVPN_RUNTIME_DIR/state.json}"

ovpn_critical_mode() {
  case "${OVPN_CRITICAL_MODE:-exit}" in
  exit | maintenance) printf '%s\n' "${OVPN_CRITICAL_MODE:-exit}" ;;
  *) ovpn_die 'OVPN_CRITICAL_MODE must be exit or maintenance' ;;
  esac
}

ovpn_runtime_write_state() {
  local instance_state="$1"
  local daemon_state="$2"
  local maintenance="$3"
  local temporary_path

  mkdir -p "$OVPN_RUNTIME_DIR"
  chmod 750 "$OVPN_RUNTIME_DIR"
  temporary_path="$OVPN_RUNTIME_STATE_FILE.tmp"
  umask 077
  cat >"$temporary_path" <<EOF_STATE
{
  "service": "openvpn",
  "instance_state": "$instance_state",
  "daemon": "$daemon_state",
  "maintenance": $maintenance
}
EOF_STATE
  mv "$temporary_path" "$OVPN_RUNTIME_STATE_FILE"
  chmod 640 "$OVPN_RUNTIME_STATE_FILE"
}

ovpn_status_command() {
  local state

  [ "$#" -eq 0 ] || {
    ovpn_log 'usage: ovpn runtime status'
    exit 64
  }
  if [ -r "$OVPN_RUNTIME_STATE_FILE" ]; then
    cat "$OVPN_RUNTIME_STATE_FILE"
    return 0
  fi

  state="$(ovpn_state_detect)"
  printf '{\n  "service": "openvpn",\n  "instance_state": "%s",\n  "daemon": "unknown",\n  "maintenance": false\n}\n' "$state"
}

ovpn_healthcheck_command() {
  [ "$#" -eq 0 ] || {
    ovpn_log 'usage: ovpn runtime health'
    return 64
  }
  if [ ! -r "$OVPN_RUNTIME_STATE_FILE" ]; then
    ovpn_log 'runtime state is unavailable'
    return 1
  fi
  if grep -Fq '"maintenance": true' "$OVPN_RUNTIME_STATE_FILE"; then
    ovpn_log 'instance is in maintenance mode'
    return 1
  fi
  if ! grep -Fq '"instance_state": "HEALTHY"' "$OVPN_RUNTIME_STATE_FILE" || ! grep -Fq '"daemon": "running"' "$OVPN_RUNTIME_STATE_FILE"; then
    ovpn_log 'runtime state is not healthy'
    return 1
  fi
  if [ ! -c /dev/net/tun ]; then
    ovpn_log 'TUN device is unavailable'
    return 1
  fi
  if ! pgrep -x openvpn >/dev/null 2>&1; then
    ovpn_log 'OpenVPN daemon is not running'
    return 1
  fi
  if ! ovpn_management_socket_request "$OVPN_MANAGEMENT_SOCKET" broker-health |
    grep -Fq 'SUCCESS: broker connected to OpenVPN'; then
    ovpn_log 'OpenVPN management broker is unavailable'
    return 1
  fi
}
ovpn_maintenance_enter() {
  local state="$1"

  ovpn_runtime_write_state "$state" stopped true
  ovpn_log "instance state is $state; entering maintenance mode"
  ovpn_log 'recommended: docker compose run --rm openvpn-maintenance state doctor'
  ovpn_log 'recommended: docker compose run --rm openvpn-maintenance repair plan'
  trap 'ovpn_log "maintenance mode stopping"; exit 0' INT TERM
  while :; do
    sleep 3600 &
    wait "$!" || true
  done
}
