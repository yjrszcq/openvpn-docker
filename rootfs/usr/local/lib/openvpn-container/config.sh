#!/usr/bin/env bash

OVPN_CONFIG_DIR="${OVPN_CONFIG_DIR:-$OVPN_DATA_DIR/config}"
OVPN_PROJECT_ENV="${OVPN_PROJECT_ENV:-$OVPN_CONFIG_DIR/project.env}"
OVPN_SCHEMA_VERSION_FILE="${OVPN_SCHEMA_VERSION_FILE:-$OVPN_CONFIG_DIR/schema-version}"

ovpn_config_defaults() {
  OVPN_CONFIG_VERSION=2
  OVPN_ENDPOINT="${OVPN_ENDPOINT:-}"
  OVPN_PROTO="${OVPN_PROTO:-udp}"
  OVPN_PORT="${OVPN_PORT:-1194}"
  OVPN_NETWORK="${OVPN_NETWORK:-10.8.0.0/24}"
  OVPN_TOPOLOGY="${OVPN_TOPOLOGY:-subnet}"
  OVPN_DYNAMIC_POOL_SIZE="${OVPN_DYNAMIC_POOL_SIZE:-}"
  OVPN_NAT="${OVPN_NAT:-true}"
  OVPN_NAT_INTERFACE="${OVPN_NAT_INTERFACE:-auto}"
  OVPN_REDIRECT_GATEWAY="${OVPN_REDIRECT_GATEWAY:-false}"
  OVPN_CLIENT_TO_CLIENT="${OVPN_CLIENT_TO_CLIENT:-false}"
  OVPN_DNS="${OVPN_DNS:-}"
  OVPN_ROUTES="${OVPN_ROUTES:-}"
}

ovpn_validate_single_line() {
  local name="$1"
  local value="$2"

  case "$value" in
    *$'\n'*|*$'\r'*) ovpn_die "$name must not contain a newline" ;;
  esac
}

ovpn_validate_bootstrap_endpoint() {
  if ! [[ "$OVPN_ENDPOINT" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]*$ ]]; then
    ovpn_die "OVPN_ENDPOINT must be a hostname or IP address"
  fi
}

ovpn_config_normalize_bootstrap() {
  ovpn_config_defaults
  ovpn_validate_bootstrap_endpoint
  ovpn_config_validate
}

ovpn_config_set_key() {
  local key="$1"
  local value="$2"

  case "$key" in
    OVPN_CONFIG_VERSION|OVPN_ENDPOINT|OVPN_PROTO|OVPN_PORT|OVPN_NETWORK|OVPN_TOPOLOGY|OVPN_DYNAMIC_POOL_SIZE|OVPN_NAT|OVPN_NAT_INTERFACE|OVPN_REDIRECT_GATEWAY|OVPN_CLIENT_TO_CLIENT|OVPN_DNS|OVPN_ROUTES)
      printf -v "$key" '%s' "$value"
      ;;
    ''|'#'*)
      ;;
    *)
      ovpn_die "unsupported config key '$key' in $OVPN_PROJECT_ENV"
      ;;
  esac
}

ovpn_config_load_file() {
  local line key value

  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      ''|'#'*) continue ;;
    esac
    if [[ "$line" != *=* ]]; then
      ovpn_die "invalid config line in $OVPN_PROJECT_ENV: $line"
    fi
    key="${line%%=*}"
    value="${line#*=}"
    ovpn_config_set_key "$key" "$value"
  done <"$OVPN_PROJECT_ENV"
}

ovpn_validate_bool() {
  local name="$1"
  local value="$2"
  case "$value" in
    true|false) ;;
    *) ovpn_die "$name must be true or false" ;;
  esac
}

ovpn_validate_port() {
  if ! [[ "$OVPN_PORT" =~ ^[0-9]+$ ]]; then
    ovpn_die "OVPN_PORT must be numeric"
  fi
  if [ "$OVPN_PORT" -lt 1 ] || [ "$OVPN_PORT" -gt 65535 ]; then
    ovpn_die "OVPN_PORT must be between 1 and 65535"
  fi
}

ovpn_validate_proto() {
  case "$OVPN_PROTO" in
    udp|tcp) ;;
    *) ovpn_die "OVPN_PROTO must be udp or tcp" ;;
  esac
}

ovpn_validate_cidr() {
  local cidr="$1"
  local ip prefix octet o1 o2 o3 o4

  if ! [[ "$cidr" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$ ]]; then
    ovpn_die "invalid IPv4 CIDR: $cidr"
  fi

  ip="${cidr%/*}"
  prefix="${cidr#*/}"
  IFS=. read -r o1 o2 o3 o4 <<<"$ip"
  for octet in "$o1" "$o2" "$o3" "$o4"; do
    if [ "$octet" -gt 255 ]; then
      ovpn_die "invalid IPv4 CIDR: $cidr"
    fi
  done

  if [ "$prefix" -lt 0 ] || [ "$prefix" -gt 32 ]; then
    ovpn_die "invalid IPv4 prefix: $cidr"
  fi
}

ovpn_validate_ipv4() {
  local address="$1"
  local octet o1 o2 o3 o4

  if ! [[ "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    ovpn_die "invalid IPv4 address: $address"
  fi

  IFS=. read -r o1 o2 o3 o4 <<<"$address"
  for octet in "$o1" "$o2" "$o3" "$o4"; do
    if [ "$octet" -gt 255 ]; then
      ovpn_die "invalid IPv4 address: $address"
    fi
  done
}

ovpn_validate_csv_cidrs() {
  local value="$1"
  local item
  local -a items

  [ -z "$value" ] && return 0
  case "$value" in
    ,*|*,|*,,*) ovpn_die 'OVPN_ROUTES must be a comma-separated list of IPv4 CIDRs' ;;
  esac
  IFS=, read -ra items <<<"$value"
  for item in "${items[@]}"; do
    ovpn_validate_cidr "$item"
  done
}

ovpn_validate_dns_servers() {
  local value="$1"
  local item
  local -a items

  [ -z "$value" ] && return 0
  case "$value" in
    ,*|*,|*,,*) ovpn_die 'OVPN_DNS must be a comma-separated list of IPv4 addresses' ;;
  esac
  IFS=, read -ra items <<<"$value"
  for item in "${items[@]}"; do
    ovpn_validate_ipv4 "$item"
  done
}

ovpn_validate_nat_interface() {
  case "$OVPN_NAT_INTERFACE" in
    auto) return 0 ;;
  esac
  if ! [[ "$OVPN_NAT_INTERFACE" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$ ]]; then
    ovpn_die 'OVPN_NAT_INTERFACE must be auto or a Linux interface name'
  fi
}

ovpn_config_validate() {
  case "$OVPN_CONFIG_VERSION" in
    1|2) ;;
    *) ovpn_die "unsupported OVPN_CONFIG_VERSION: $OVPN_CONFIG_VERSION" ;;
  esac
  ovpn_validate_single_line OVPN_ENDPOINT "$OVPN_ENDPOINT"
  ovpn_validate_single_line OVPN_NAT_INTERFACE "$OVPN_NAT_INTERFACE"
  ovpn_validate_single_line OVPN_DNS "$OVPN_DNS"
  ovpn_validate_single_line OVPN_ROUTES "$OVPN_ROUTES"
  ovpn_validate_proto
  ovpn_validate_port
  case "$OVPN_TOPOLOGY" in
    subnet) ;;
    *) ovpn_die 'OVPN_TOPOLOGY must be subnet' ;;
  esac
  ovpn_ipam_calculate_layout "$OVPN_NETWORK" "$OVPN_DYNAMIC_POOL_SIZE"
  OVPN_DYNAMIC_POOL_SIZE="$OVPN_IPAM_DYNAMIC_POOL_SIZE"
  ovpn_validate_bool OVPN_NAT "$OVPN_NAT"
  ovpn_validate_bool OVPN_REDIRECT_GATEWAY "$OVPN_REDIRECT_GATEWAY"
  ovpn_validate_bool OVPN_CLIENT_TO_CLIENT "$OVPN_CLIENT_TO_CLIENT"
  ovpn_validate_nat_interface
  ovpn_validate_csv_cidrs "$OVPN_ROUTES"
  ovpn_validate_dns_servers "$OVPN_DNS"
}

ovpn_config_load() {
  ovpn_config_defaults
  if [ -f "$OVPN_PROJECT_ENV" ]; then
    ovpn_config_load_file
  fi
  ovpn_config_validate
}

ovpn_config_print() {
  ovpn_config_load
  cat <<EOF
OVPN_CONFIG_VERSION=$OVPN_CONFIG_VERSION
OVPN_ENDPOINT=$OVPN_ENDPOINT
OVPN_PROTO=$OVPN_PROTO
OVPN_PORT=$OVPN_PORT
OVPN_NETWORK=$OVPN_NETWORK
OVPN_TOPOLOGY=$OVPN_TOPOLOGY
OVPN_DYNAMIC_POOL_SIZE=$OVPN_DYNAMIC_POOL_SIZE
OVPN_NAT=$OVPN_NAT
OVPN_NAT_INTERFACE=$OVPN_NAT_INTERFACE
OVPN_REDIRECT_GATEWAY=$OVPN_REDIRECT_GATEWAY
OVPN_CLIENT_TO_CLIENT=$OVPN_CLIENT_TO_CLIENT
OVPN_DNS=$OVPN_DNS
OVPN_ROUTES=$OVPN_ROUTES
EOF
}

ovpn_config_write_loaded() {
  mkdir -p "$OVPN_CONFIG_DIR"
  umask 077
  cat >"$OVPN_PROJECT_ENV.tmp" <<EOF
OVPN_CONFIG_VERSION=$OVPN_CONFIG_VERSION
OVPN_ENDPOINT=$OVPN_ENDPOINT
OVPN_PROTO=$OVPN_PROTO
OVPN_PORT=$OVPN_PORT
OVPN_NETWORK=$OVPN_NETWORK
OVPN_TOPOLOGY=$OVPN_TOPOLOGY
OVPN_DYNAMIC_POOL_SIZE=$OVPN_DYNAMIC_POOL_SIZE
OVPN_NAT=$OVPN_NAT
OVPN_NAT_INTERFACE=$OVPN_NAT_INTERFACE
OVPN_REDIRECT_GATEWAY=$OVPN_REDIRECT_GATEWAY
OVPN_CLIENT_TO_CLIENT=$OVPN_CLIENT_TO_CLIENT
OVPN_DNS=$OVPN_DNS
OVPN_ROUTES=$OVPN_ROUTES
EOF
  mv "$OVPN_PROJECT_ENV.tmp" "$OVPN_PROJECT_ENV"
  printf '%s\n' "$OVPN_CONFIG_VERSION" >"$OVPN_SCHEMA_VERSION_FILE"
  chmod 600 "$OVPN_PROJECT_ENV" "$OVPN_SCHEMA_VERSION_FILE"
}

ovpn_config_write() {
  ovpn_config_normalize_bootstrap
  ovpn_config_write_loaded
}

ovpn_config_command() {
  local subcommand="${1:-print}"
  if [ "$#" -gt 0 ]; then
    shift
  fi

  case "$subcommand" in
    print)
      ovpn_config_print "$@"
      ;;
    init)
      ovpn_config_write "$@"
      ;;
    *)
      ovpn_log "unknown config subcommand '$subcommand'"
      ovpn_log "usage: ovpn config [print|init]"
      exit 64
      ;;
  esac
}
