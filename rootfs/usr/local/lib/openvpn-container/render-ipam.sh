#!/usr/bin/env bash

OVPN_LEASE_DIR="${OVPN_LEASE_DIR:-$OVPN_DATA_DIR/data/leases}"
OVPN_MANAGEMENT_SOCKET="${OVPN_MANAGEMENT_SOCKET:-$OVPN_RUNTIME_DIR/management.sock}"
OVPN_OPENVPN_MANAGEMENT_SOCKET="${OVPN_OPENVPN_MANAGEMENT_SOCKET:-$OVPN_RUNTIME_DIR/openvpn-management.sock}"

OVPN_MANAGEMENT_GREETING_DELAY_SECONDS="${OVPN_MANAGEMENT_GREETING_DELAY_SECONDS:-0.05}"

ovpn_management_socket_request() {
  local socket="$1"
  local request="$2"
  local socat_bin="${OVPN_SOCAT_BIN:-socat}"
  local delay="$OVPN_MANAGEMENT_GREETING_DELAY_SECONDS"
  local response

  [ -S "$socket" ] || return 1
  command -v "$socat_bin" >/dev/null 2>&1 || return 1
  [[ "$delay" =~ ^(0|[1-9][0-9]*)(\.[0-9]+)?$ ]] || return 1
  response="$(
    {
      sleep "$delay"
      printf '%s\n' "$request"
      sleep "$delay"
      printf 'quit\n'
    } | "$socat_bin" -T 5 -,ignoreeof "UNIX-CONNECT:$socket" 2>&1
  )" || return 1
  printf '%s\n' "$response"
}

ovpn_prepare_ipam_render_context() {
  local dynamic_start dynamic_end

  # Consumed by render.sh after this sourced helper returns.
  # shellcheck disable=SC2034
  OVPN_CCD_DIR="$OVPN_RENDER_DATA_DIR/ccd"
  OVPN_DYNAMIC_POOL_DIRECTIVE=''
  if [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ]; then
    dynamic_start="$(ovpn_ipam_int_to_ipv4 "$OVPN_IPAM_DYNAMIC_START_INT")"
    dynamic_end="$(ovpn_ipam_int_to_ipv4 "$OVPN_IPAM_DYNAMIC_END_INT")"
    if [ -z "$dynamic_start" ] || [ -z "$dynamic_end" ]; then
      ovpn_die "failed to compute dynamic pool range"
    fi
    # Consumed by render.sh after this sourced helper returns.
    # shellcheck disable=SC2034
    OVPN_DYNAMIC_POOL_DIRECTIVE="ifconfig-pool $dynamic_start $dynamic_end"
  fi
}
