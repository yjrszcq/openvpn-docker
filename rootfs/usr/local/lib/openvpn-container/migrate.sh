#!/usr/bin/env bash

OVPN_MIGRATION_DIR="${OVPN_MIGRATION_DIR:-$LIB_DIR/migrations}"
# shellcheck source=/usr/local/lib/openvpn-container/migration-transaction.sh
. "$LIB_DIR/migration-transaction.sh"

ovpn_migrate_usage() {
  cat <<'EOF'
Usage: ovpn migrate <command> [options]

Commands:
  plan              inspect the required schema migration
  apply             apply a planned migration in maintenance mode

Options:
  --to-version VERSION  target management release
  --json                print a machine-readable plan
  --yes                 skip apply confirmation
EOF
}

ovpn_migrate_require_maintenance() {
  [ "${OVPN_MAINTENANCE:-false}" = true ] || {
    ovpn_log 'migrate is available only through the openvpn-maintenance service'
    return 78
  }
}

ovpn_migrate_load_step() {
  local source_schema="$1"
  local target_schema="$2"
  local step="$OVPN_MIGRATION_DIR/$source_schema-to-$target_schema.sh"

  [ -r "$step" ] || {
    ovpn_log "missing migration step $source_schema-to-$target_schema"
    return 78
  }
  # Historical compatibility is intentionally loaded only from this dispatcher.
  # shellcheck source=/dev/null
  . "$step"
}

ovpn_migrate_print_plan() {
  local json="$1"
  local source_schema="$2"
  local target_schema="$3"
  local chain="$4"
  local client_count="$5"
  local blocked="$6"
  local reason="$7"

  if [ "$json" = true ]; then
    printf '{"source_schema":%s,"target_schema":%s,"chain":"%s","clients":%s,"blocked":%s,"reason":"%s"}\n' \
      "$source_schema" "$target_schema" "$chain" "$client_count" "$blocked" "$reason"
  else
    printf 'source schema: %s\n' "$source_schema"
    printf 'target schema: %s\n' "$target_schema"
    printf 'migration chain: %s\n' "$chain"
    printf 'clients: %s\n' "$client_count"
    printf 'blocked: %s\n' "$blocked"
    [ -z "$reason" ] || printf 'reason: %s\n' "$reason"
  fi
}

ovpn_migrate_plan() {
  local json="$1"

  ovpn_schema_probe
  case "$OVPN_SCHEMA_STATUS" in
    CURRENT)
      ovpn_migrate_print_plan "$json" "$OVPN_CURRENT_DATA_SCHEMA" "$OVPN_CURRENT_DATA_SCHEMA" none 0 false ''
      ;;
    OLD)
      if [ "$OVPN_SCHEMA_PROJECT_VERSION" = 1 ] && [ "$OVPN_SCHEMA_FILE_VERSION" = 1 ]; then
        ovpn_migrate_load_step 1 2 || return $?
        if [ "$OVPN_CURRENT_DATA_SCHEMA" = 2 ]; then
          ovpn_migrate_print_plan "$json" 1 2 1-to-2 "$(ovpn_migration_1_to_2_client_count)" false \
            'deleted tombstones did not exist in schema 1 and cannot be recovered'
        else
          ovpn_migrate_print_plan "$json" 1 "$OVPN_CURRENT_DATA_SCHEMA" '1-to-2;2-to-3 unavailable' \
            "$(ovpn_migration_1_to_2_client_count)" true \
            'schema 1 deleted tombstones cannot be recovered; no complete migration chain is registered'
          return 78
        fi
      else
        ovpn_migrate_print_plan "$json" "${OVPN_SCHEMA_PROJECT_VERSION:-0}" "$OVPN_CURRENT_DATA_SCHEMA" unavailable 0 true 'no registered migration chain'
        return 78
      fi
      ;;
    *)
      ovpn_migrate_print_plan "$json" "${OVPN_SCHEMA_PROJECT_VERSION:-0}" "$OVPN_CURRENT_DATA_SCHEMA" unavailable 0 true "schema status is $OVPN_SCHEMA_STATUS"
      return 78
      ;;
  esac
}

ovpn_migrate_command() {
  local subcommand="${1:-}"
  local json=false yes=false target_version=''

  if ovpn_help_requested "$@"; then
    ovpn_migrate_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die 'usage: ovpn migrate <plan|apply>'
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --json) json=true ;;
      --yes) yes=true ;;
      --to-version)
        shift
        [ "$#" -gt 0 ] || ovpn_die 'missing value for --to-version'
        target_version="$1"
        ;;
      *) ovpn_die "unknown migrate option '$1'" ;;
    esac
    shift
  done
  [ -z "$target_version" ] || ovpn_die 'target management release selection is not available until signed bundles are enabled'
  ovpn_migrate_require_maintenance || return $?
  case "$subcommand" in
    plan)
      [ "$yes" = false ] || ovpn_die '--yes is valid only with migrate apply'
      ovpn_migrate_plan "$json"
      ;;
    apply)
      [ "$json" = false ] || ovpn_die '--json is valid only with migrate plan'
      ovpn_schema_probe
      [ "$OVPN_SCHEMA_STATUS" = CURRENT ] && return 0
      [ "$yes" = true ] || ovpn_die 'migrate apply requires --yes when transaction support is enabled'
      ovpn_log "migrate apply is blocked until a complete migration chain to schema $OVPN_CURRENT_DATA_SCHEMA is registered"
      return 78
      ;;
    *) ovpn_die 'usage: ovpn migrate <plan|apply>' ;;
  esac
}
