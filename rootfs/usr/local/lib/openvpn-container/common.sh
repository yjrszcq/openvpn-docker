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
  help              show this help
USAGE
}

ovpn_version() {
  if [ -r "$OVPN_BUILD_INFO" ]; then
    cat "$OVPN_BUILD_INFO"
  else
    cat <<'JSON'
{
  "image_version": "unknown",
  "runtime_strategy": "unknown",
  "openvpn_version": "unknown",
  "easy_rsa_version": "unknown",
  "supported_openvpn_range": "unknown"
}
JSON
  fi
}


ovpn_runtime_command() {
  local subcommand="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_runtime_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die "usage: ovpn runtime <status|health|capabilities|version>"
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
    *) ovpn_die "usage: ovpn runtime <status|health|capabilities|version>" ;;
  esac
}
