#!/usr/bin/env bash

OVPN_POOL_PERSIST_FILE="${OVPN_POOL_PERSIST_FILE:-/var/lib/openvpn/pool-persist.txt}"
OVPN_MANAGEMENT_SOCKET="${OVPN_MANAGEMENT_SOCKET:-$OVPN_RUNTIME_DIR/management.sock}"

ovpn_prepare_ipam_render_context() {
  local dynamic_start dynamic_end

  OVPN_CCD_DIR="$OVPN_RENDER_DATA_DIR/ccd"
  OVPN_DYNAMIC_POOL_DIRECTIVE=''
  if [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ]; then
    dynamic_start="$(ovpn_ipam_int_to_ipv4 "$OVPN_IPAM_DYNAMIC_START_INT")"
    dynamic_end="$(ovpn_ipam_int_to_ipv4 "$OVPN_IPAM_DYNAMIC_END_INT")"
    OVPN_DYNAMIC_POOL_DIRECTIVE="ifconfig-pool $dynamic_start $dynamic_end"
  fi
}
