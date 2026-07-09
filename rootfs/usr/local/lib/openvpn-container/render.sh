#!/usr/bin/env bash

OVPN_TEMPLATE_DIR="${OVPN_TEMPLATE_DIR:-/usr/local/share/openvpn-container/templates/openvpn-2.7}"
OVPN_RENDER_DATA_DIR="${OVPN_RENDER_DATA_DIR:-$OVPN_DATA_DIR}"

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
  template="${template//\{\{OVPN_PROTO\}\}/$OVPN_PROTO}"
  template="${template//\{\{OVPN_ENDPOINT\}\}/$OVPN_ENDPOINT}"
  template="${template//\{\{OVPN_DATA_DIR\}\}/$OVPN_RENDER_DATA_DIR}"
  template="${template//\{\{OVPN_NETWORK_ADDRESS\}\}/$OVPN_NETWORK_ADDRESS}"
  template="${template//\{\{OVPN_NETWORK_NETMASK\}\}/$OVPN_NETWORK_NETMASK}"
  template="${template//\{\{OVPN_CLIENT_TO_CLIENT_DIRECTIVE\}\}/$OVPN_CLIENT_TO_CLIENT_DIRECTIVE}"
  template="${template//\{\{OVPN_REDIRECT_GATEWAY_PUSH\}\}/$OVPN_REDIRECT_GATEWAY_PUSH}"
  template="${template//\{\{OVPN_ROUTE_PUSHES\}\}/$OVPN_ROUTE_PUSHES}"
  template="${template//\{\{OVPN_DNS_PUSHES\}\}/$OVPN_DNS_PUSHES}"
  template="${template//\{\{CA_CERT\}\}/$CA_CERT}"
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
  ovpn_config_load
  OVPN_NETWORK_ADDRESS="$(ovpn_cidr_ip "$OVPN_NETWORK")"
  OVPN_NETWORK_NETMASK="$(ovpn_cidr_netmask "$OVPN_NETWORK")"

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
  local template_path="$OVPN_TEMPLATE_DIR/server.conf.tpl"
  [ -r "$template_path" ] || ovpn_die "missing server template: $template_path"
  ovpn_prepare_render_context
  CA_CERT=""
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
  local template_path="$OVPN_TEMPLATE_DIR/client.ovpn.tpl"
  [ -r "$template_path" ] || ovpn_die "missing client template: $template_path"
  ovpn_validate_client_name "$client_name"
  ovpn_prepare_render_context

  if [ -z "$OVPN_ENDPOINT" ]; then
    ovpn_die "OVPN_ENDPOINT is required to render client profiles"
  fi

  CA_CERT="$(ovpn_read_required_file "$OVPN_DATA_DIR/pki/ca.crt")"
  CLIENT_CERT="$(ovpn_read_required_file "$OVPN_DATA_DIR/pki/issued/$client_name.crt")"
  CLIENT_KEY="$(ovpn_read_required_file "$OVPN_DATA_DIR/pki/private/$client_name.key")"
  TLS_CRYPT_KEY="$(ovpn_read_required_file "$OVPN_DATA_DIR/secrets/tls-crypt.key")"
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
      --stdout)
        output_path='-'
        ;;
      --output)
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
  local client_name="${1:-}"
  local output_path='-'
  [ -n "$client_name" ] || ovpn_die "usage: ovpn render client <name> [--stdout|--output path]"
  shift

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --stdout)
        output_path='-'
        ;;
      --output)
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
  ovpn_write_or_print "$output_path" "$(ovpn_render_client_content "$client_name")"
}

ovpn_render_command() {
  local target="${1:-}"
  [ -n "$target" ] || ovpn_die "usage: ovpn render <server|client> ..."
  shift

  case "$target" in
    server)
      ovpn_render_server "$@"
      ;;
    client)
      ovpn_render_client "$@"
      ;;
    *)
      ovpn_log "unknown render target '$target'"
      ovpn_log "usage: ovpn render <server|client> ..."
      exit 64
      ;;
  esac
}
