#!/usr/bin/env bash

OVPN_IP_BIN="${OVPN_IP_BIN:-ip}"
OVPN_IPTABLES_BIN="${OVPN_IPTABLES_BIN:-iptables}"
OVPN_IP_FORWARD_FILE="${OVPN_IP_FORWARD_FILE:-/proc/sys/net/ipv4/ip_forward}"

ovpn_network_default_interface() {
  local interface

  interface="$("$OVPN_IP_BIN" -4 route show default 2>/dev/null | awk '
    /^default/ {
      for (field = 1; field <= NF; field++) {
        if ($field == "dev" && field < NF) {
          print $(field + 1)
          exit
        }
      }
    }
  ')"
  [ -n "$interface" ] || ovpn_die 'could not determine the NAT egress interface'
  OVPN_NAT_INTERFACE="$interface"
  ovpn_validate_nat_interface
  printf '%s\n' "$interface"
}

ovpn_network_egress_interface() {
  if [ "$OVPN_NAT_INTERFACE" = auto ]; then
    ovpn_network_default_interface
  else
    printf '%s\n' "$OVPN_NAT_INTERFACE"
  fi
}

ovpn_network_requires_forwarding() {
  [ "$OVPN_NAT" = true ] || \
    [ "$OVPN_REDIRECT_GATEWAY" = true ] || \
    [ -n "$OVPN_ROUTES" ]
}

ovpn_network_enable_ipv4_forwarding() {
  local current

  [ -r "$OVPN_IP_FORWARD_FILE" ] || ovpn_die "IPv4 forwarding control is unavailable: $OVPN_IP_FORWARD_FILE"
  current="$(cat "$OVPN_IP_FORWARD_FILE")"
  [ "$current" = 1 ] && return 0
  [ -w "$OVPN_IP_FORWARD_FILE" ] || ovpn_die "IPv4 forwarding is disabled and cannot be enabled: $OVPN_IP_FORWARD_FILE"
  printf '1\n' >"$OVPN_IP_FORWARD_FILE" || ovpn_die 'failed to enable IPv4 forwarding'
  [ "$(cat "$OVPN_IP_FORWARD_FILE")" = 1 ] || ovpn_die 'IPv4 forwarding did not become enabled'
}

ovpn_network_ensure_iptables_rule() {
  local table="$1"
  local chain="$2"
  shift 2

  if [ -n "$table" ]; then
    "$OVPN_IPTABLES_BIN" -w -t "$table" -C "$chain" "$@" >/dev/null 2>&1 || \
      "$OVPN_IPTABLES_BIN" -w -t "$table" -A "$chain" "$@"
  else
    "$OVPN_IPTABLES_BIN" -w -C "$chain" "$@" >/dev/null 2>&1 || \
      "$OVPN_IPTABLES_BIN" -w -A "$chain" "$@"
  fi
}

ovpn_network_configure() {
  local interface

  ovpn_config_load
  ovpn_network_requires_forwarding || return 0
  ovpn_network_enable_ipv4_forwarding
  interface="$(ovpn_network_egress_interface)"

  ovpn_network_ensure_iptables_rule '' FORWARD -s "$OVPN_NETWORK" -o "$interface" -j ACCEPT
  ovpn_network_ensure_iptables_rule '' FORWARD -d "$OVPN_NETWORK" -i "$interface" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
  if [ "$OVPN_NAT" = true ]; then
    ovpn_network_ensure_iptables_rule nat POSTROUTING -s "$OVPN_NETWORK" -o "$interface" -j MASQUERADE
  fi
}
