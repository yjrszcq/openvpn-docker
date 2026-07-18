#!/usr/bin/env bash
set -euo pipefail

LIB_DIR="${OVPN_LIB_DIR:-/usr/local/lib/openvpn-container}"
# shellcheck source=/usr/local/lib/openvpn-container/common.sh
. "$LIB_DIR/common.sh"
. "$LIB_DIR/schema.sh"
. "$LIB_DIR/ipam.sh"
. "$LIB_DIR/config.sh"
. "$LIB_DIR/registry.sh"
. "$LIB_DIR/client-ip.sh"
. "$LIB_DIR/network.sh"
. "$LIB_DIR/render.sh"
. "$LIB_DIR/render-ipam.sh"
. "$LIB_DIR/lock.sh"
. "$LIB_DIR/client-ip-sync.sh"
. "$LIB_DIR/recovery.sh"
. "$LIB_DIR/state.sh"
. "$LIB_DIR/repair.sh"
. "$LIB_DIR/state-ipam.sh"
. "$LIB_DIR/pki.sh"
. "$LIB_DIR/compatibility.sh"
. "$LIB_DIR/metadata.sh"
. "$LIB_DIR/init.sh"
. "$LIB_DIR/maintenance.sh"
. "$LIB_DIR/start.sh"
. "$LIB_DIR/client.sh"
. "$LIB_DIR/client-ip-mutation.sh"
. "$LIB_DIR/client-lifecycle.sh"
. "$LIB_DIR/network-migration.sh"

command="${1:-help}"
if [ "$#" -gt 0 ]; then
  shift
fi

ovpn_schema_gate_command "$command" "$@" || exit $?

case "$command" in
  help|-h|--help) ovpn_usage ;;
  -v) ovpn_version_short ;;
  --version) ovpn_version_summary ;;
  init)
    if ovpn_help_requested "$@"; then
      ovpn_command_usage "ovpn init" "Initialize an empty OpenVPN data directory."
    else
      ovpn_init_command "$@"
    fi
    ;;
  start)
    if ovpn_help_requested "$@"; then
      ovpn_command_usage "ovpn start" "Scan state and start OpenVPN."
    else
      ovpn_start_command "$@"
    fi
    ;;
  config) ovpn_config_command "$@" ;;
  client) ovpn_client_command "$@" ;;
  network) ovpn_network_command "$@" ;;
  repair) ovpn_repair_command "$@" ;;
  state) ovpn_state_command "$@" ;;
  render) ovpn_render_command "$@" ;;
  runtime) ovpn_runtime_command "$@" ;;
  upgrade)
    . "$LIB_DIR/upgrade.sh"
    ovpn_upgrade_command "$@"
    ;;
  migrate)
    # Historical parsers are lazy-loaded only by the migration dispatcher.
    . "$LIB_DIR/migrate.sh"
    ovpn_migrate_command "$@"
    ;;
  *)
    ovpn_log "unknown command '$command'"
    ovpn_usage >&2
    exit 64
    ;;
esac
