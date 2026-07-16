#!/usr/bin/env bash
# OpenVPN client-connect / client-disconnect hook for dynamic-client lease tracking.
# Called by OpenVPN with environment variables: $common_name, $ifconfig_pool_remote_ip, $trusted_ip.
# First argument is the pool-persist file path.

set -euo pipefail

pool_file="${1:-$OVPN_DATA_DIR/data/pool-persist.txt}"

pool_hook_upsert() {
  local name="$1"
  local address="$2"
  local tmpfile line existing_name

  [ -n "$name" ] || return 0
  [ -n "$address" ] || return 0
  mkdir -p "$(dirname "$pool_file")"
  tmpfile="$(mktemp "$(dirname "$pool_file")/.pool-persist.XXXXXX")"
  if [ -f "$pool_file" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      existing_name="${line%%,*}"
      # Remove old entry for this client name, and also remove any other
      # client that had this IP — OpenVPN just reassigned it to us.
      [ "$existing_name" = "$name" ] && continue
      [ "${line#*,}" = "$address" ] && continue
      printf '%s\n' "$line"
    done <"$pool_file" >"$tmpfile"
  fi
  printf '%s,%s\n' "$name" "$address" >>"$tmpfile"
  mv "$tmpfile" "$pool_file"
  chmod 600 "$pool_file"
}

pool_hook_remove() {
  local name="$1"
  local tmpfile

  [ -n "$name" ] || return 0
  [ -f "$pool_file" ] || return 0
  tmpfile="$(mktemp "$(dirname "$pool_file")/.pool-persist.XXXXXX")"
  grep -v "^${name}," "$pool_file" >"$tmpfile" 2>/dev/null || true
  mv "$tmpfile" "$pool_file"
  chmod 600 "$pool_file"
}

case "${script_type:-}" in
  client-connect)
    pool_hook_upsert "${common_name:-}" "${ifconfig_pool_remote_ip:-}"
    ;;
  client-disconnect)
    # Keep the last-known entry; OpenVPN will re-read this file on restart.
    # If the client reconnects, client-connect will update the address.
    ;;
esac

exit 0
