#!/usr/bin/env bash

OVPN_DATA_DIR="${OVPN_DATA_DIR:-/etc/openvpn}"
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
  doctor            inspect state without modifying it
  state             print the detected state
  repair            plan or run safe repairs
  recover           run explicit high-risk recovery actions
  config            inspect or update persistent project config
  render            render derived configuration
  add-client        create a client certificate
  export-client     write a client profile to stdout
  list-clients      list known clients
  revoke-client     revoke a client certificate
  status            print runtime status
  healthcheck       return container health
  capabilities      print runtime capability information
  version           print build information
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

ovpn_not_implemented() {
  local command="$1"
  ovpn_log "command '$command' is not implemented in this phase"
  exit 2
}
