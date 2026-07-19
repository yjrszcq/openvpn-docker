#!/usr/bin/env bash
# OpenVPN client-connect / client-disconnect hook for dynamic-client lease tracking.
# Called by OpenVPN with environment variables: $common_name, $ifconfig_pool_remote_ip, $trusted_ip.
# Also expects $OVPN_LEASE_DIR to be set in OpenVPN's environment.
#
# Each client gets its own file named by UUID common name, containing the IP address.
# This eliminates read-modify-write races with sync code and other hook instances.

set -euo pipefail

lease_dir="${OVPN_LEASE_DIR:-/etc/openvpn/cache/client-leases}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/usr/local/lib/openvpn-container/events.sh
. "$SCRIPT_DIR/events.sh"

pool_hook_upsert() {
  local name="$1"
  local address="$2"
  local f ccd_dir

  [[ "$name" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] || return 1
  [ -n "$address" ] || return 0

  # Static clients have a CCD file with ifconfig-push — skip lease tracking.
  ccd_dir="${OVPN_DATA_DIR:-/etc/openvpn}/ccd"
  [ -f "$ccd_dir/$name" ] && return 0

  mkdir -p "$lease_dir"

  # If OpenVPN just reassigned this IP from another client to us, remove
  # the stale lease file so list --detail won't show a zombie last-known.
  for f in "$lease_dir"/*; do
    [ -f "$f" ] || continue
    [ "$(cat "$f" 2>/dev/null || true)" = "$address" ] && rm -f "$f"
  done

  printf '%s\n' "$address" >"$lease_dir/.$name.tmp"
  mv "$lease_dir/.$name.tmp" "$lease_dir/$name"
  chmod 600 "$lease_dir/$name"
}

case "${script_type:-}" in
  client-connect)
    pool_hook_upsert "${common_name:-}" "${ifconfig_pool_remote_ip:-}"
    ;;
  client-disconnect)
    # Keep the last-known entry; the file stays.
    ;;
esac

if [[ "${common_name:-}" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
  client_name="$(ovpn_event_identity_name "$common_name" || true)"
  details="$(
    jq -cn \
      --arg virtual_ip "${ifconfig_pool_remote_ip:-}" \
      --arg remote_ip "${trusted_ip:-}" \
      --arg remote_port "${trusted_port:-}" \
      --arg bytes_received "${bytes_received:-}" \
      --arg bytes_sent "${bytes_sent:-}" \
      --arg duration_seconds "${time_duration:-}" '
        {
          virtual_ip: (if $virtual_ip == "" then null else $virtual_ip end),
          remote_ip: (if $remote_ip == "" then null else $remote_ip end),
          remote_port: (if $remote_port == "" then null else $remote_port end),
          bytes_received: (if $bytes_received == "" then null else ($bytes_received | tonumber) end),
          bytes_sent: (if $bytes_sent == "" then null else ($bytes_sent | tonumber) end),
          duration_seconds: (if $duration_seconds == "" then null else ($duration_seconds | tonumber) end)
        }
      '
  )" || details='{}'
  ovpn_event_write client_connection \
    "${script_type#client-}" applied "$common_name" "$client_name" "$details" || true
fi

exit 0
