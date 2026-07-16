#!/usr/bin/env bash
# OpenVPN client-connect / client-disconnect hook for dynamic-client lease tracking.
# Called by OpenVPN with environment variables: $common_name, $ifconfig_pool_remote_ip, $trusted_ip.
# First argument is the lease directory path.
#
# Each client gets its own file named by common name, containing the IP address.
# This eliminates read-modify-write races with sync code and other hook instances.

set -euo pipefail

lease_dir="${1:-$OVPN_DATA_DIR/data/leases}"

pool_hook_upsert() {
  local name="$1"
  local address="$2"
  local f

  [ -n "$name" ] || return 0
  [ -n "$address" ] || return 0
  mkdir -p "$lease_dir"

  # If OpenVPN just reassigned this IP from another client to us, remove
  # the stale lease file so list --detail won't show a zombie last-known.
  for f in "$lease_dir"/*; do
    [ -f "$f" ] || continue
    [ "$(cat "$f")" = "$address" ] && rm -f "$f"
  done

  printf '%s\n' "$address" >"$lease_dir/$name"
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

exit 0
