#!/usr/bin/env bash

# IPv4 allocation helpers for the supported TUN subnet topology. The server
# reserves network+1, leaving network+2 through broadcast-1 for clients.

ovpn_ipam_ipv4_to_int() {
  local address="$1"
  local o1 o2 o3 o4 octet

  if ! [[ "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    return 1
  fi

  IFS=. read -r o1 o2 o3 o4 <<<"$address"
  for octet in "$o1" "$o2" "$o3" "$o4"; do
    [[ "$octet" =~ ^0$|^[1-9][0-9]{0,2}$ ]] || return 1
    [ "$((10#$octet))" -le 255 ] || return 1
  done

  printf '%u\n' "$(( (10#$o1 << 24) | (10#$o2 << 16) | (10#$o3 << 8) | 10#$o4 ))"
}

ovpn_ipam_int_to_ipv4() {
  local value="$1"

  [[ "$value" =~ ^[0-9]+$ ]] || return 1
  [ "$value" -le 4294967295 ] || return 1
  printf '%d.%d.%d.%d\n' \
    "$(((value >> 24) & 255))" \
    "$(((value >> 16) & 255))" \
    "$(((value >> 8) & 255))" \
    "$((value & 255))"
}

ovpn_ipam_calculate_layout() {
  local cidr="$1"
  local requested_pool_size="${2:-}"
  local address prefix address_int mask host_bits total_addresses client_total pool_size

  [[ "$cidr" =~ ^(.+)/([0-9]{1,2})$ ]] || ovpn_die "invalid IPv4 CIDR: $cidr"
  address="${BASH_REMATCH[1]}"
  prefix="$((10#${BASH_REMATCH[2]}))"
  address_int="$(ovpn_ipam_ipv4_to_int "$address")" || ovpn_die "invalid IPv4 CIDR: $cidr"
  if [ "$prefix" -gt 30 ]; then
    ovpn_die 'OVPN_NETWORK must provide at least one client address (/30 or larger)'
  fi

  if [ "$prefix" -eq 0 ]; then
    mask=0
  else
    mask="$(( (0xffffffff << (32 - prefix)) & 0xffffffff ))"
  fi
  if [ "$((address_int & mask))" -ne "$address_int" ]; then
    ovpn_die "OVPN_NETWORK must be a canonical network CIDR: $cidr"
  fi

  host_bits="$((32 - prefix))"
  total_addresses="$((1 << host_bits))"
  client_total="$((total_addresses - 3))"
  if [ -z "$requested_pool_size" ]; then
    pool_size="$((client_total / 2))"
  elif [[ "$requested_pool_size" =~ ^[0-9]+$ ]]; then
    pool_size="$((10#$requested_pool_size))"
  else
    ovpn_die 'OVPN_DYNAMIC_POOL_SIZE must be a non-negative integer'
  fi
  if [ "$pool_size" -gt "$client_total" ]; then
    ovpn_die "OVPN_DYNAMIC_POOL_SIZE must be between 0 and $client_total"
  fi

  OVPN_IPAM_NETWORK="$cidr"
  OVPN_IPAM_NETWORK_INT="$address_int"
  OVPN_IPAM_PREFIX="$prefix"
  OVPN_IPAM_NETMASK="$(ovpn_ipam_int_to_ipv4 "$mask")"
  OVPN_IPAM_SERVER_IP="$(ovpn_ipam_int_to_ipv4 "$((address_int + 1))")"
  OVPN_IPAM_CLIENT_START_INT="$((address_int + 2))"
  OVPN_IPAM_CLIENT_END_INT="$((address_int + total_addresses - 2))"
  OVPN_IPAM_CLIENT_CAPACITY="$client_total"
  OVPN_IPAM_DYNAMIC_POOL_SIZE="$pool_size"
  OVPN_IPAM_STATIC_CAPACITY="$((client_total - pool_size))"
  OVPN_IPAM_STATIC_START_INT="$OVPN_IPAM_CLIENT_START_INT"
  if [ "$pool_size" -eq 0 ]; then
    OVPN_IPAM_STATIC_END_INT="$OVPN_IPAM_CLIENT_END_INT"
    OVPN_IPAM_DYNAMIC_START_INT=''
    OVPN_IPAM_DYNAMIC_END_INT=''
  else
    OVPN_IPAM_DYNAMIC_START_INT="$((OVPN_IPAM_CLIENT_END_INT - pool_size + 1))"
    OVPN_IPAM_DYNAMIC_END_INT="$OVPN_IPAM_CLIENT_END_INT"
    OVPN_IPAM_STATIC_END_INT="$((OVPN_IPAM_DYNAMIC_START_INT - 1))"
  fi
}

ovpn_ipam_ip_in_static_range() {
  local value

  [ "$OVPN_IPAM_STATIC_CAPACITY" -gt 0 ] || return 1
  value="$(ovpn_ipam_ipv4_to_int "$1")" || return 1
  [ "$value" -ge "$OVPN_IPAM_STATIC_START_INT" ] && [ "$value" -le "$OVPN_IPAM_STATIC_END_INT" ]
}

ovpn_ipam_ip_in_dynamic_pool() {
  local value

  [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ] || return 1
  value="$(ovpn_ipam_ipv4_to_int "$1")" || return 1
  [ "$value" -ge "$OVPN_IPAM_DYNAMIC_START_INT" ] && [ "$value" -le "$OVPN_IPAM_DYNAMIC_END_INT" ]
}
