#!/usr/bin/env bash

OVPN_DATA_DIR="${OVPN_DATA_DIR:-/etc/openvpn}"
OVPN_RUNTIME_DIR="${OVPN_RUNTIME_DIR:-/run/openvpn-container}"
OVPN_SERVER_NAME="${OVPN_SERVER_NAME:-openvpn-server}"
ovpn_openssl_bin() {
  if [ -n "${OVPN_OPENSSL_BIN:-}" ]; then
    printf '%s\n' "$OVPN_OPENSSL_BIN"
    return 0
  fi
  command -v openssl
}

ovpn_exit_for_state() {
  case "$1" in
    CRITICAL|UNRECOVERABLE) exit 78 ;;
    *) exit 1 ;;
  esac
}

OVPN_BUILD_INFO="${OVPN_BUILD_INFO:-/usr/local/share/openvpn-container/build-info.json}"

ovpn_log() {
  printf 'ovpn: %s\n' "$*" >&2
}

ovpn_die() {
  ovpn_log "$*"
  exit 1
}


ovpn_help_requested() {
  [ "$#" -eq 1 ] || return 1
  case "$1" in
    -h|--help) return 0 ;;
    *) return 1 ;;
  esac
}

ovpn_command_usage() {
  printf "Usage: %s\n\n%s\n" "$1" "$2"
}

ovpn_config_usage() {
  cat <<EOF
Usage: ovpn config <command>

Commands:
  show              print persisted project configuration
  apply             validate environment and write project configuration

Run ovpn config <command> --help for command details.
EOF
}

ovpn_client_usage() {
  cat <<EOF
Usage: ovpn client <command> [args]

Commands:
  create            create a client certificate and profile
  export            write a client profile to stdout
  list              list client certificates and assignments
  rename            change a client's display name
  revoke            revoke a client certificate
  reissue           issue a new certificate for an existing client
  delete            remove a client and its local credentials
  ip                manage client IP assignments

Run ovpn client <command> --help for command details.
EOF
}

ovpn_client_ip_usage() {
  cat <<EOF
Usage: ovpn client ip <command> [args]

Commands:
  release           release the retained static IP of a revoked client
  set               assign client IP addresses

Run ovpn client ip <command> --help for command details.
EOF
}

ovpn_network_usage() {
  cat <<EOF
Usage: ovpn network <command> [options]

Commands:
  plan              preview a tunnel-network migration
  apply             apply a tunnel-network migration

Options:
  --network CIDR            target tunnel network
  --dynamic-pool-size N     target dynamic-pool size
  --yes                     skip the apply confirmation prompt
EOF
}

ovpn_repair_usage() {
  cat <<EOF
Usage: ovpn repair <command>

Commands:
  plan              inspect eligible repair actions
  apply             apply eligible repair actions

Run ovpn repair plan --help for JSON output details.
EOF
}

ovpn_state_usage() {
  cat <<EOF
Usage: ovpn state <command>

Commands:
  show              print the detected instance state
  doctor            print detected issues and recommended actions

Run ovpn state doctor --help for JSON output details.
EOF
}

ovpn_render_usage() {
  cat <<EOF
Usage: ovpn render <target> [options]

Targets:
  server            render the server configuration
  client            render a client profile

Run ovpn render <target> --help for output options.
EOF
}

ovpn_runtime_usage() {
  cat <<EOF
Usage: ovpn runtime <command>

Commands:
  status            print runtime state JSON
  health            return container health status
  capabilities      print runtime capability information
  version           print build information
  logs              read or follow persistent OpenVPN logs
  events            read or follow structured runtime events
EOF
}

ovpn_usage() {
  cat <<'USAGE'
Usage: ovpn <command> [args]

Commands:
  start             scan policy and start OpenVPN
  init              initialize an empty data directory
  config            show or apply persistent project configuration
  client            manage client certificates and IP assignments
  network           plan or apply a tunnel-network migration
  repair            plan or apply safe recovery actions
  state             show instance state or run diagnostics
  render            render derived server or client configuration
  runtime           inspect runtime status, health, capabilities, or version
  migrate           plan or apply an offline data-schema migration
  help              show this help

Options:
  -h, --help        show this help
  -v                print only the image version
  -V, --version     print the operator-facing version summary
USAGE
}

ovpn_easyrsa_version() {
  local easyrsa_bin ver
  easyrsa_bin="$(ovpn_easyrsa_bin 2>/dev/null)" || true
  if [ -n "$easyrsa_bin" ] && [ -x "$easyrsa_bin" ]; then
    ver="$("$easyrsa_bin" --version 2>/dev/null | grep -oE 'Version:[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+' | head -1 | sed 's/Version:[[:space:]]*//')"
    if [ -n "$ver" ]; then
      printf '%s\n' "$ver"
      return 0
    fi
  fi
  printf 'unknown\n'
}

ovpn_version() {
  local easyrsa_ver
  easyrsa_ver="$(ovpn_easyrsa_version)"
  if [ -r "$OVPN_BUILD_INFO" ]; then
    awk -v easyrsa="$easyrsa_ver" '
      BEGIN { OFS = "" }
      /"easy_rsa_version"/ { sub(/: *"[^"]*"/, ": \"" easyrsa "\"") }
      { print }
    ' "$OVPN_BUILD_INFO"
  else
    cat <<JSON
{
  "image_version": "unknown",
  "data_schema": "unknown",
  "runtime_strategy": "unknown",
  "openvpn_version": "unknown",
  "easy_rsa_version": "$easyrsa_ver",
  "openvpn_candidate_range": "unknown"
}
JSON
  fi
}

ovpn_version_short() {
  local info="${OVPN_BUILD_INFO:-/usr/local/share/openvpn-container/build-info.json}"
  if [ -r "$info" ]; then
    grep -o '"image_version": *"[^"]*"' "$info" | head -1 | sed 's/.*: *"//;s/"//'
  else
    printf 'unknown\n'
  fi

}

ovpn_version_summary() {
  local info="${OVPN_BUILD_INFO:-/usr/local/share/openvpn-container/build-info.json}"
  local image_ver='unknown' schema='unknown' ovpn_ver='unknown' easyrsa_ver='unknown'
  local label_width=15

  if [ -r "$info" ]; then
    image_ver="$(grep -o '"image_version": *"[^"]*"' "$info" | head -1 | sed 's/.*: *"//;s/"//')"
    [ -n "$image_ver" ] || image_ver=unknown
    schema="$(grep -o '"data_schema": *[0-9]*' "$info" | head -1 | sed 's/.*: *//')"
    [ -n "$schema" ] || schema=unknown
    ovpn_ver="$(grep -o '"openvpn_version": *"[^"]*"' "$info" | head -1 | sed 's/.*: *"//;s/"//')"
    [ -n "$ovpn_ver" ] || ovpn_ver=unknown
  fi

  easyrsa_ver="$(ovpn_easyrsa_version)"

  printf '%-*s  %s\n' "$label_width" 'image:' "$image_ver"
  printf '%-*s  %s\n' "$label_width" 'openvpn:' "$ovpn_ver"
  printf '%-*s  %s\n' "$label_width" 'easy-rsa:' "$easyrsa_ver"
  printf '%-*s  %s\n' "$label_width" 'data schema:' "$schema"
}


ovpn_runtime_command() {
  local subcommand="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_runtime_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die "usage: ovpn runtime <status|health|capabilities|version|logs|events>"
  shift
  case "$subcommand" in
    status)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn runtime status" "Print runtime state as JSON."
      else
        ovpn_status_command "$@"
      fi
      ;;
    health)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn runtime health" "Return success only when the container is healthy."
      else
        ovpn_healthcheck_command "$@"
      fi
      ;;
    capabilities)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn runtime capabilities" "Print OpenVPN compatibility and feature information."
      else
        [ "$#" -eq 0 ] || ovpn_die "usage: ovpn runtime capabilities"
        ovpn_capabilities_command
      fi
      ;;
    version)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn runtime version" "Print image and runtime build information."
      else
        [ "$#" -eq 0 ] || ovpn_die "usage: ovpn runtime version"
        ovpn_version
      fi
      ;;
    logs)
      command -v "${OVPN_PYTHON_BIN:-python3}" >/dev/null 2>&1 ||
        ovpn_die "python3 is required to read OpenVPN logs"
      "${OVPN_PYTHON_BIN:-python3}" "$LIB_DIR/runtime-logs.py" \
        "$@" \
        --log-file "${OVPN_RAW_LOG_FILE:-$OVPN_DATA_DIR/logs/openvpn.log}" \
        --registry "$(ovpn_registry_client_state_file)"
      ;;
    events)
      command -v "${OVPN_PYTHON_BIN:-python3}" >/dev/null 2>&1 ||
        ovpn_die "python3 is required to read runtime events"
      "${OVPN_PYTHON_BIN:-python3}" "$LIB_DIR/runtime-events.py" \
        "$@" \
        --event-file "${OVPN_EVENTS_FILE:-$OVPN_DATA_DIR/logs/events.jsonl}"
      ;;
    *) ovpn_die "usage: ovpn runtime <status|health|capabilities|version|logs|events>" ;;
  esac
}
