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

  [ -n "$subcommand" ] || ovpn_die 'usage: ovpn runtime <status|health|capabilities|version>'
  shift
  case "$subcommand" in
    status) ovpn_status_command "$@" ;;
    health) ovpn_healthcheck_command "$@" ;;
    capabilities) [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn runtime capabilities'; ovpn_capabilities_command ;;
    version) [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn runtime version'; ovpn_version ;;
    *) ovpn_die 'usage: ovpn runtime <status|health|capabilities|version>' ;;
  esac
}
