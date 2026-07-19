#!/usr/bin/env bash

OVPN_TEMPLATE_ROOT="${OVPN_TEMPLATE_ROOT:-/usr/local/share/openvpn-container/templates}"
OVPN_RENDER_DATA_DIR="${OVPN_RENDER_DATA_DIR:-$OVPN_DATA_DIR}"

ovpn_template_dir() {
  local family

  family="$(ovpn_compatibility_template_family)" || return 1
  printf '%s/%s\n' "$OVPN_TEMPLATE_ROOT" "$family"
}

ovpn_cidr_ip() {
  printf '%s\n' "${1%/*}"
}

ovpn_cidr_netmask() {
  local prefix="${1#*/}"
  local mask=$((0xffffffff << (32 - prefix) & 0xffffffff))
  printf '%d.%d.%d.%d\n' \
    $(((mask >> 24) & 255)) \
    $(((mask >> 16) & 255)) \
    $(((mask >> 8) & 255)) \
    $((mask & 255))
}

ovpn_template_apply() {
  local template="$1"
  template="${template//\{\{OVPN_PORT\}\}/$OVPN_PORT}"
  template="${template//\{\{OVPN_SERVER_PROTO\}\}/$OVPN_SERVER_PROTO}"
  template="${template//\{\{OVPN_BIND_DIRECTIVE\}\}/$OVPN_BIND_DIRECTIVE}"
  template="${template//\{\{OVPN_CLIENT_PROTO\}\}/$OVPN_CLIENT_PROTO}"
  template="${template//\{\{OVPN_ENDPOINT\}\}/$OVPN_ENDPOINT}"
  template="${template//\{\{OVPN_DATA_DIR\}\}/$OVPN_RENDER_DATA_DIR}"
  template="${template//\{\{OVPN_NETWORK_ADDRESS\}\}/$OVPN_NETWORK_ADDRESS}"
  template="${template//\{\{OVPN_NETWORK_NETMASK\}\}/$OVPN_NETWORK_NETMASK}"
  template="${template//\{\{OVPN_CCD_DIR\}\}/$OVPN_CCD_DIR}"
  template="${template//\{\{OVPN_DYNAMIC_POOL_DIRECTIVE\}\}/$OVPN_DYNAMIC_POOL_DIRECTIVE}"
  template="${template//\{\{OVPN_LEASE_DIR\}\}/$OVPN_LEASE_DIR}"
  template="${template//\{\{OVPN_MANAGEMENT_SOCKET\}\}/$OVPN_MANAGEMENT_SOCKET}"
  template="${template//\{\{OVPN_OPENVPN_MANAGEMENT_SOCKET\}\}/$OVPN_OPENVPN_MANAGEMENT_SOCKET}"
  template="${template//\{\{OVPN_CLIENT_TO_CLIENT_DIRECTIVE\}\}/$OVPN_CLIENT_TO_CLIENT_DIRECTIVE}"
  template="${template//\{\{OVPN_REDIRECT_GATEWAY_PUSH\}\}/$OVPN_REDIRECT_GATEWAY_PUSH}"
  template="${template//\{\{OVPN_ROUTE_PUSHES\}\}/$OVPN_ROUTE_PUSHES}"
  template="${template//\{\{OVPN_DNS_PUSHES\}\}/$OVPN_DNS_PUSHES}"
  template="${template//\{\{CA_CERT\}\}/$CA_CERT}"
  template="${template//\{\{CLIENT_ID\}\}/$CLIENT_ID}"
  template="${template//\{\{CLIENT_NAME\}\}/$CLIENT_NAME}"
  template="${template//\{\{CLIENT_CERT\}\}/$CLIENT_CERT}"
  template="${template//\{\{CLIENT_KEY\}\}/$CLIENT_KEY}"
  template="${template//\{\{TLS_CRYPT_KEY\}\}/$TLS_CRYPT_KEY}"
  printf '%s\n' "$template"
}

ovpn_join_pushes() {
  local kind="$1"
  local values="$2"
  local output=""
  local item ip mask
  local -a items

  if [ -z "$values" ]; then
    return 0
  fi

  IFS=, read -ra items <<<"$values"
  for item in "${items[@]}"; do
    [ -n "$item" ] || continue
    case "$kind" in
    route)
      ovpn_validate_cidr "$item"
      ip="$(ovpn_cidr_ip "$item")"
      mask="$(ovpn_cidr_netmask "$item")"
      output+="push \"route $ip $mask\""$'\n'
      ;;
    dns)
      output+="push \"dhcp-option DNS $item\""$'\n'
      ;;
    esac
  done
  printf '%s' "$output"
}

ovpn_prepare_render_context() {
  local transport_family

  ovpn_config_load
  transport_family="$OVPN_TRANSPORT_FAMILY"
  if [ "$transport_family" = auto ]; then
    if ovpn_ipam_ipv4_to_int "$OVPN_ENDPOINT" >/dev/null 2>&1; then
      transport_family=ipv4
    elif [[ "$OVPN_ENDPOINT" == *:* ]]; then
      transport_family=ipv6
    fi
  fi
  case "$transport_family:$OVPN_PROTO" in
  auto:udp)
    OVPN_SERVER_PROTO=udp6
    OVPN_CLIENT_PROTO=udp
    ;;
  auto:tcp)
    OVPN_SERVER_PROTO=tcp6-server
    OVPN_CLIENT_PROTO=tcp
    ;;
  ipv4:udp)
    OVPN_SERVER_PROTO=udp4
    OVPN_CLIENT_PROTO=udp4
    ;;
  ipv4:tcp)
    OVPN_SERVER_PROTO=tcp4-server
    OVPN_CLIENT_PROTO=tcp4-client
    ;;
  ipv6:udp)
    OVPN_SERVER_PROTO=udp6
    OVPN_CLIENT_PROTO=udp6
    ;;
  ipv6:tcp)
    OVPN_SERVER_PROTO=tcp6-server
    OVPN_CLIENT_PROTO=tcp6-client
    ;;
  esac
  OVPN_BIND_DIRECTIVE=""
  if [ "$transport_family" = ipv6 ]; then
    OVPN_BIND_DIRECTIVE="bind ipv6only"
  fi

  OVPN_NETWORK_ADDRESS="$(ovpn_cidr_ip "$OVPN_NETWORK")"
  OVPN_NETWORK_NETMASK="$(ovpn_cidr_netmask "$OVPN_NETWORK")"
  ovpn_prepare_ipam_render_context

  OVPN_CLIENT_TO_CLIENT_DIRECTIVE=""
  if [ "$OVPN_CLIENT_TO_CLIENT" = true ]; then
    OVPN_CLIENT_TO_CLIENT_DIRECTIVE="client-to-client"
  fi

  OVPN_REDIRECT_GATEWAY_PUSH=""
  if [ "$OVPN_REDIRECT_GATEWAY" = true ]; then
    OVPN_REDIRECT_GATEWAY_PUSH='push "redirect-gateway def1"'
  fi

  OVPN_ROUTE_PUSHES="$(ovpn_join_pushes route "$OVPN_ROUTES")"
  OVPN_DNS_PUSHES="$(ovpn_join_pushes dns "$OVPN_DNS")"
}

ovpn_render_server_content() {
  local template_dir template_path
  template_dir="$(ovpn_template_dir)" || ovpn_die "no compatible template family for OpenVPN runtime"
  template_path="$template_dir/server.conf.tpl"
  [ -r "$template_path" ] || ovpn_die "missing server template: $template_path"
  ovpn_prepare_render_context
  CA_CERT=""
  CLIENT_ID=""
  CLIENT_NAME=""
  CLIENT_CERT=""
  CLIENT_KEY=""
  TLS_CRYPT_KEY=""
  ovpn_template_apply "$(cat "$template_path")"
}

ovpn_read_required_file() {
  local path="$1"
  [ -r "$path" ] || ovpn_die "missing required file: $path"
  cat "$path"
}

ovpn_validate_client_name() {
  local name="$1"
  if ! [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]; then
    ovpn_die "invalid client name: $name"
  fi
}

ovpn_render_client_content() {
  local client_name="$1"
  local client_id template_dir template_path
  template_dir="$(ovpn_template_dir)" || ovpn_die "no compatible template family for OpenVPN runtime"
  template_path="$template_dir/client.ovpn.tpl"
  [ -r "$template_path" ] || ovpn_die "missing client template: $template_path"
  ovpn_validate_client_name "$client_name"
  client_id="$(ovpn_registry_current_id_by_name "$client_name")" || ovpn_die "current client identity is missing for '$client_name'"
  ovpn_prepare_render_context

  if [ -z "$OVPN_ENDPOINT" ]; then
    ovpn_die "OVPN_ENDPOINT is required to render client profiles"
  fi

  CA_CERT="$(ovpn_read_required_file "$OVPN_DATA_DIR/pki/ca.crt")" || exit 1
  CLIENT_ID="$client_id"
  CLIENT_NAME="$client_name"
  CLIENT_CERT="$(ovpn_read_required_file "$OVPN_DATA_DIR/pki/issued/$client_id.crt")" || exit 1
  CLIENT_KEY="$(ovpn_read_required_file "$OVPN_DATA_DIR/pki/private/$client_id.key")" || exit 1
  TLS_CRYPT_KEY="$(ovpn_read_required_file "$OVPN_DATA_DIR/secrets/tls-crypt.key")" || exit 1
  ovpn_template_apply "$(cat "$template_path")"
}

ovpn_write_or_print() {
  local output_path="$1"
  local content="$2"

  if [ "$output_path" = '-' ]; then
    printf '%s\n' "$content"
    return 0
  fi

  mkdir -p "$(dirname "$output_path")"
  umask 077
  printf '%s\n' "$content" >"$output_path.tmp"
  mv "$output_path.tmp" "$output_path"
  chmod 600 "$output_path"
}

ovpn_render_server() {
  local output_path="$OVPN_DATA_DIR/server/server.conf"
  while [ "$#" -gt 0 ]; do
    case "$1" in
    --stdout|-s)
      output_path='-'
      ;;
    --output|-o)
      shift
      [ "$#" -gt 0 ] || ovpn_die "--output requires a path"
      output_path="$1"
      ;;
    *)
      ovpn_die "unknown render server argument: $1"
      ;;
    esac
    shift
  done
  ovpn_write_or_print "$output_path" "$(ovpn_render_server_content)"
}

ovpn_render_client() {
  local usage='usage: ovpn render client <client>|--id <ID>|--name <NAME> [--stdout|-s|--output|-o <path>]'
  local selector_mode client_reference consumed
  local client_name
  local output_path='-'

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  client_reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"

  while [ "$#" -gt 0 ]; do
    case "$1" in
    --stdout|-s)
      output_path='-'
      ;;
    --output|-o)
      shift
      [ "$#" -gt 0 ] || ovpn_die "--output requires a path"
      output_path="$1"
      ;;
    *)
      ovpn_die "unknown render client argument: $1"
      ;;
    esac
    shift
  done
  ovpn_client_resolve_selector_or_die "$selector_mode" "$client_reference"
  client_name="$OVPN_CLIENT_RESOLVED_NAME"
  ovpn_write_or_print "$output_path" "$(ovpn_render_client_content "$client_name")"
}

ovpn_render_command() {
  local target="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_render_usage
    return 0
  fi
  [ -n "$target" ] || ovpn_die "usage: ovpn render <server|client> ..."
  shift

  case "$target" in
  server)
    if ovpn_help_requested "$@"; then
      ovpn_command_usage "ovpn render server [--stdout|-s|--output|-o <path>]" "Render the server configuration."
    else
      ovpn_render_server "$@"
    fi
    ;;
  client)
    if ovpn_help_requested "$@"; then
      ovpn_command_usage "ovpn render client <client>|--id <ID>|--name <NAME> [--stdout|-s|--output|-o <path>]" "Render a client profile."
    else
      ovpn_render_client "$@"
    fi
    ;;
  *)
    ovpn_log "unknown render target '$target'"
    ovpn_log "usage: ovpn render <server|client> ..."
    exit 64
    ;;
  esac
}
