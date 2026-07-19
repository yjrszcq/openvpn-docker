#!/usr/bin/env bash

OVPN_MIGRATION_DIR="${OVPN_MIGRATION_DIR:-$LIB_DIR/migrations}"
OVPN_MIGRATE_APPLY_SOURCE=''
# shellcheck source=/usr/local/lib/openvpn-container/migration-transaction.sh
. "$LIB_DIR/migration-transaction.sh"

ovpn_migrate_usage() {
  cat <<'EOF'
Usage: ovpn migrate <command> [options]

Commands:
  plan              inspect the required schema migration
  apply             apply a planned migration in maintenance mode

Options:
  --json, -j        print a machine-readable plan
  --yes, -y         skip apply confirmation
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
    ovpn_migrate_print_plan "$json" "$OVPN_CURRENT_DATA_SCHEMA" \
      "$OVPN_CURRENT_DATA_SCHEMA" none 0 false ''
    ;;
  OLD)
    if [ "$OVPN_SCHEMA_PROJECT_VERSION" = 1 ] &&
      [ "$OVPN_SCHEMA_FILE_VERSION" = 1 ]; then
      ovpn_migrate_load_step 1 2 || return $?
      ovpn_migrate_load_step 2 3 || return $?
      ovpn_migrate_print_plan "$json" 1 3 '1-to-2;2-to-3' \
        "$(ovpn_migration_1_to_2_client_count)" false \
        'all active profiles will be replaced; schema 1 deleted tombstones cannot be recovered'
    elif [ "$OVPN_SCHEMA_PROJECT_VERSION" = 2 ] &&
      [ "$OVPN_SCHEMA_FILE_VERSION" = 2 ]; then
      ovpn_migrate_load_step 2 3 || return $?
      ovpn_migrate_print_plan "$json" 2 3 2-to-3 \
        "$(ovpn_migration_2_to_3_client_count)" false \
        'all active profiles will be replaced and must be redistributed'
    else
      ovpn_migrate_print_plan "$json" "${OVPN_SCHEMA_PROJECT_VERSION:-0}" \
        "$OVPN_CURRENT_DATA_SCHEMA" unavailable 0 true \
        'no registered migration chain'
      return 78
    fi
    ;;
  *)
    ovpn_migrate_print_plan "$json" "${OVPN_SCHEMA_PROJECT_VERSION:-0}" \
      "$OVPN_CURRENT_DATA_SCHEMA" unavailable 0 true \
      "schema status is $OVPN_SCHEMA_STATUS"
    return 78
    ;;
  esac
}

ovpn_migrate_apply_stage() {
  local stage="$1"

  if [ "$OVPN_MIGRATE_APPLY_SOURCE" = 1 ]; then
    ovpn_migration_1_to_2_apply_staged "$stage"
    ovpn_migration_1_to_2_validate_staged "$stage"
  fi
  ovpn_migration_2_to_3_apply_staged "$stage" "$OVPN_DATA_DIR"
}

ovpn_migrate_validate_current() {
  ovpn_migration_2_to_3_validate_staged "$1"
}

ovpn_migrate_recover_interrupted() {
  if [ -e "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env" ]; then
    ovpn_with_runtime_exclusive_lock \
      ovpn_with_data_lock migration \
      ovpn_migration_transaction_recover_interrupted
  fi
}

ovpn_migrate_confirm_apply() {
  local answer

  if [ ! -t 0 ]; then
    ovpn_log 'migrate apply requires --yes in non-interactive mode'
    return 64
  fi
  printf 'Migrate persistent data to schema %s? [y/N] ' \
    "$OVPN_CURRENT_DATA_SCHEMA" >&2
  IFS= read -r answer || return 64
  case "$answer" in y | Y | yes | YES) return 0 ;; esac
  ovpn_log 'migration cancelled'
  return 64
}

ovpn_migrate_report_profiles() {
  local id name state

  while IFS=, read -r id name state; do
    [ "$id" = '# id' ] && continue
    [ "$state" = active ] || continue
    printf 'redistribute profile: %s\n' \
      "$OVPN_DATA_DIR/clients/active/$name.ovpn"
  done <"$OVPN_DATA_DIR/meta/client-state.csv"
}

ovpn_migrate_command() {
  local subcommand="${1:-}"
  local json=false yes=false

  if ovpn_help_requested "$@"; then
    ovpn_migrate_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die 'usage: ovpn migrate <plan|apply>'
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
    --json|-j) json=true ;;
    --yes|-y) yes=true ;;
    *) ovpn_die "unknown migrate option '$1'" ;;
    esac
    shift
  done
  ovpn_migrate_require_maintenance || return $?
  case "$subcommand" in
  plan)
    [ "$yes" = false ] || ovpn_die '--yes is valid only with migrate apply'
    if [ -e "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env" ]; then
      ovpn_schema_probe
      ovpn_migrate_print_plan "$json" \
        "${OVPN_SCHEMA_PROJECT_VERSION:-0}" "$OVPN_CURRENT_DATA_SCHEMA" \
        recovery 0 true \
        'an interrupted migration must be recovered by migrate apply'
      return 78
    fi
    ovpn_migrate_plan "$json"
    ;;
  apply)
    [ "$json" = false ] || ovpn_die '--json is valid only with migrate plan'
    ovpn_migrate_recover_interrupted
    ovpn_schema_probe
    [ "$OVPN_SCHEMA_STATUS" != CURRENT ] || return 0
    [ "$OVPN_SCHEMA_STATUS" = OLD ] || {
      ovpn_log "cannot migrate while schema status is $OVPN_SCHEMA_STATUS"
      return 78
    }
    [ "$OVPN_SCHEMA_PROJECT_VERSION" = "$OVPN_SCHEMA_FILE_VERSION" ] || {
      ovpn_log 'schema metadata conflicts; refusing migration'
      return 78
    }
    OVPN_MIGRATE_APPLY_SOURCE="$OVPN_SCHEMA_PROJECT_VERSION"
    case "$OVPN_MIGRATE_APPLY_SOURCE" in
    1)
      ovpn_migrate_load_step 1 2 || return $?
      ovpn_migrate_load_step 2 3 || return $?
      ;;
    2)
      ovpn_migrate_load_step 2 3 || return $?
      ;;
    *)
      ovpn_log "no migration chain from schema $OVPN_MIGRATE_APPLY_SOURCE"
      return 78
      ;;
    esac
    [ "$yes" = true ] || ovpn_migrate_confirm_apply || return $?
    ovpn_migration_transaction_run "$OVPN_MIGRATE_APPLY_SOURCE" \
      "$OVPN_CURRENT_DATA_SCHEMA" ovpn_migrate_apply_stage \
      ovpn_migrate_validate_current
    ovpn_migrate_report_profiles
    ;;
  *) ovpn_die 'usage: ovpn migrate <plan|apply>' ;;
  esac
}
