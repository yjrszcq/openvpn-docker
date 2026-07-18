#!/usr/bin/env bash

OVPN_MIGRATION_DIR="${OVPN_MIGRATION_DIR:-$LIB_DIR/migrations}"
OVPN_MIGRATE_APPLY_SOURCE=''
OVPN_MIGRATE_TARGET_VERSION=''
OVPN_MIGRATE_TARGET_DIR=''
OVPN_MIGRATE_TARGET_ROOT=''
OVPN_MIGRATE_TARGET_WORK=''
OVPN_MIGRATE_OLD_ACTIVE=''
OVPN_MIGRATE_OLD_PREVIOUS=''
OVPN_MIGRATE_MANAGEMENT_LOCK_HELD=false
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
  local management_version="${8:-}"

  if [ "$json" = true ]; then
    printf '{"source_schema":%s,"target_schema":%s,"chain":"%s","clients":%s,"blocked":%s,"reason":"%s","management_version":%s}\n' \
      "$source_schema" "$target_schema" "$chain" "$client_count" "$blocked" "$reason" \
      "$([ -n "$management_version" ] && printf '"%s"' "$management_version" || printf null)"
  else
    printf 'source schema: %s\n' "$source_schema"
    printf 'target schema: %s\n' "$target_schema"
    printf 'migration chain: %s\n' "$chain"
    printf 'clients: %s\n' "$client_count"
    printf 'blocked: %s\n' "$blocked"
    [ -z "$reason" ] || printf 'reason: %s\n' "$reason"
    [ -z "$management_version" ] || printf 'target management version: %s\n' "$management_version"
  fi
}

ovpn_migrate_plan() {
  local json="$1"
  local management_version="${2:-}"

  ovpn_schema_probe
  case "$OVPN_SCHEMA_STATUS" in
  CURRENT)
    ovpn_migrate_print_plan "$json" "$OVPN_CURRENT_DATA_SCHEMA" "$OVPN_CURRENT_DATA_SCHEMA" none 0 false ''
    ;;
  OLD)
    if [ "$OVPN_SCHEMA_PROJECT_VERSION" = 1 ] && [ "$OVPN_SCHEMA_FILE_VERSION" = 1 ]; then
      ovpn_migrate_load_step 1 2 || return $?
      ovpn_migrate_load_step 2 3 || return $?
      ovpn_migrate_print_plan "$json" 1 3 '1-to-2;2-to-3' \
        "$(ovpn_migration_1_to_2_client_count)" false \
        'all active profiles will be replaced; schema 1 deleted tombstones cannot be recovered' \
        "$management_version"
    elif [ "$OVPN_SCHEMA_PROJECT_VERSION" = 2 ] && [ "$OVPN_SCHEMA_FILE_VERSION" = 2 ]; then
      ovpn_migrate_load_step 2 3 || return $?
      ovpn_migrate_print_plan "$json" 2 3 2-to-3 \
        "$(ovpn_migration_2_to_3_client_count)" false \
        'all active profiles will be replaced and must be redistributed' \
        "$management_version"
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

ovpn_migrate_apply_stage() {
  local stage="$1"

  if [ -n "$OVPN_MIGRATE_TARGET_ROOT" ]; then
    OVPN_INTERNAL_MIGRATION_RUNNER=true \
      OVPN_LIB_DIR="$OVPN_MIGRATE_TARGET_ROOT/lib" \
      OVPN_TEMPLATE_ROOT="$OVPN_MIGRATE_TARGET_ROOT/templates" \
      OVPN_COMPATIBILITY_DIR="$OVPN_MIGRATE_TARGET_ROOT/compatibility" \
      "$OVPN_MIGRATE_TARGET_ROOT/lib/migration-runner.sh" apply \
      "$OVPN_MIGRATE_APPLY_SOURCE" "$OVPN_CURRENT_DATA_SCHEMA" "$stage" "$OVPN_DATA_DIR"
    return
  fi
  if [ "$OVPN_MIGRATE_APPLY_SOURCE" = 1 ]; then
    ovpn_migration_1_to_2_apply_staged "$stage"
    ovpn_migration_1_to_2_validate_staged "$stage"
  fi
  ovpn_migration_2_to_3_apply_staged "$stage" "$OVPN_DATA_DIR"
}

ovpn_migrate_validate_current() {
  if [ -n "$OVPN_MIGRATE_TARGET_ROOT" ]; then
    OVPN_INTERNAL_MIGRATION_RUNNER=true \
      OVPN_LIB_DIR="$OVPN_MIGRATE_TARGET_ROOT/lib" \
      OVPN_TEMPLATE_ROOT="$OVPN_MIGRATE_TARGET_ROOT/templates" \
      OVPN_COMPATIBILITY_DIR="$OVPN_MIGRATE_TARGET_ROOT/compatibility" \
      "$OVPN_MIGRATE_TARGET_ROOT/lib/migration-runner.sh" validate \
      "$OVPN_MIGRATE_APPLY_SOURCE" "$OVPN_CURRENT_DATA_SCHEMA" "$1" "$OVPN_DATA_DIR"
    return
  fi
  ovpn_migration_2_to_3_validate_staged "$1"
}

ovpn_migrate_code_transaction_file() {
  printf '%s/transactions/migration.env\n' "$OVPN_MANAGEMENT_STORE"
}

ovpn_migrate_code_transaction_write() {
  local file temporary

  file="$(ovpn_migrate_code_transaction_file)"
  temporary="${file}.$$"
  mkdir -p "$(dirname "$file")"
  chmod 700 "$OVPN_MANAGEMENT_STORE" "$(dirname "$file")"
  printf 'OLD_ACTIVE=%s\nOLD_PREVIOUS=%s\nTARGET=%s\n' \
    "$OVPN_MIGRATE_OLD_ACTIVE" "$OVPN_MIGRATE_OLD_PREVIOUS" \
    "$OVPN_MIGRATE_TARGET_VERSION" >"$temporary"
  chmod 600 "$temporary"
  mv "$temporary" "$file"
}

ovpn_migrate_code_transaction_read() {
  local file line key value
  local old_active='' old_previous='' target='' count=0

  file="$(ovpn_migrate_code_transaction_file)"
  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    [[ "$line" == *=* ]] || return 1
    key="${line%%=*}"
    value="${line#*=}"
    if [ "$value" != embedded ] && ! [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      return 1
    fi
    case "$key" in
    OLD_ACTIVE) old_active="$value" ;;
    OLD_PREVIOUS) old_previous="$value" ;;
    TARGET) target="$value" ;;
    *) return 1 ;;
    esac
    count=$((count + 1))
  done <"$file"
  [ "$count" -eq 3 ] && [ -n "$old_active" ] && [ -n "$old_previous" ] &&
    [ -n "$target" ] || return 1
  OVPN_MIGRATE_OLD_ACTIVE="$old_active"
  OVPN_MIGRATE_OLD_PREVIOUS="$old_previous"
  [ "$target" != embedded ] || return 1
  OVPN_MIGRATE_TARGET_VERSION="$target"
}

ovpn_migrate_restore_code() {
  local transaction

  transaction="$(ovpn_migrate_code_transaction_file)"
  [ -e "$transaction" ] || return 0
  ovpn_migrate_code_transaction_read || ovpn_die \
    'management migration transaction is invalid; manual recovery is required'
  # shellcheck source=/usr/local/lib/openvpn-bootstrap.sh
  . "$OVPN_BOOTSTRAP_LIB"
  mkdir -p "$OVPN_MANAGEMENT_STORE/transactions"
  if [ "$OVPN_MIGRATE_MANAGEMENT_LOCK_HELD" = false ]; then
    exec {migrate_management_fd}>"$OVPN_MANAGEMENT_STORE/.management.lock"
    flock -x -w 30 "$migrate_management_fd" ||
      ovpn_die 'failed to acquire management-code lock for migration recovery'
  fi
  rm -f "$OVPN_MANAGEMENT_STORE/transactions/activation.env"
  ovpn_upgrade_write_pointer active "$OVPN_MIGRATE_OLD_ACTIVE" ||
    ovpn_die 'failed to restore active management selector'
  ovpn_upgrade_write_pointer previous "$OVPN_MIGRATE_OLD_PREVIOUS" ||
    ovpn_die 'failed to restore previous management selector'
  if [ "$OVPN_MIGRATE_OLD_ACTIVE" = embedded ]; then
    ovpn_bootstrap_activate_embedded ||
      ovpn_die 'failed to restore embedded management runtime'
  else
    ovpn_bootstrap_activate_online "$OVPN_MIGRATE_OLD_ACTIVE" ||
      ovpn_die 'failed to restore previous online management runtime'
  fi
  rm -f "$transaction"
  [ "$OVPN_MIGRATE_MANAGEMENT_LOCK_HELD" = true ] ||
    flock -u "$migrate_management_fd"
}

ovpn_migrate_finalize_code() {
  [ "${OVPN_MIGRATE_FAIL_CODE_ACTIVATION:-}" != before ] ||
    ovpn_die 'injected management activation failure before selector commit'
  ovpn_upgrade_activate "$OVPN_MIGRATE_TARGET_VERSION" \
    "$OVPN_MIGRATE_TARGET_DIR" activate "$OVPN_MIGRATE_MANAGEMENT_LOCK_HELD"
  [ "${OVPN_MIGRATE_FAIL_CODE_ACTIVATION:-}" != after ] ||
    ovpn_die 'injected management activation failure after selector commit'
  if [ "${OVPN_MIGRATE_INTERRUPT_AFTER_CODE_ACTIVATION:-false}" = true ]; then
    kill -KILL "$BASHPID"
  fi
}

ovpn_migrate_recover_interrupted() {
  ovpn_migrate_restore_code
  if [ -e "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env" ]; then
    ovpn_with_runtime_exclusive_lock \
      ovpn_with_data_lock migration \
      ovpn_migration_transaction_recover_interrupted
  fi
}

ovpn_migrate_select_target() {
  local requested="$1" download_bundle="$2" status

  . "$LIB_DIR/upgrade.sh"
  export OVPN_UPGRADE_SCHEMA_CHANGE_TARGET="$OVPN_CURRENT_DATA_SCHEMA"
  ovpn_upgrade_runtime_facts || return 78
  OVPN_MIGRATE_TARGET_WORK="$(mktemp -d)" || return 74
  if ovpn_upgrade_select_release "$requested" "$OVPN_MIGRATE_TARGET_WORK"; then
    :
  else
    status=$?
    rm -rf "$OVPN_MIGRATE_TARGET_WORK"
    return "$status"
  fi
  [ -n "$OVPN_UPGRADE_SELECTED_VERSION" ] || {
    rm -rf "$OVPN_MIGRATE_TARGET_WORK"
    return 78
  }
  OVPN_MIGRATE_TARGET_VERSION="$OVPN_UPGRADE_SELECTED_VERSION"
  OVPN_MIGRATE_TARGET_DIR="$OVPN_UPGRADE_SELECTED_DIR"
  [ "$(ovpn_upgrade_manifest_value "$OVPN_MIGRATE_TARGET_DIR/management-release.env" DATA_SCHEMA)" = "$OVPN_CURRENT_DATA_SCHEMA" ] || {
    rm -rf "$OVPN_MIGRATE_TARGET_WORK"
    ovpn_log "target management release must use data schema $OVPN_CURRENT_DATA_SCHEMA"
    return 78
  }
  [ "$download_bundle" = true ] || return 0
  ovpn_upgrade_download_bundle "$OVPN_MIGRATE_TARGET_WORK/releases.json" \
    "$OVPN_MIGRATE_TARGET_VERSION" "$OVPN_MIGRATE_TARGET_DIR" || return $?
  ovpn_upgrade_activate "$OVPN_MIGRATE_TARGET_VERSION" \
    "$OVPN_MIGRATE_TARGET_DIR" migration-prepare || return $?
  OVPN_MIGRATE_TARGET_ROOT="$OVPN_BOOTSTRAP_SELECTED_ROOT"
  [ -x "$OVPN_MIGRATE_TARGET_ROOT/lib/migration-runner.sh" ] || return 78
}

ovpn_migrate_confirm_apply() {
  local answer

  if [ ! -t 0 ]; then
    ovpn_log 'migrate apply requires --yes in non-interactive mode'
    return 64
  fi
  printf 'Migrate persistent data to schema %s? [y/N] ' "$OVPN_CURRENT_DATA_SCHEMA" >&2
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
    printf 'redistribute profile: %s\n' "$OVPN_DATA_DIR/clients/active/$name.ovpn"
  done <"$OVPN_DATA_DIR/meta/client-state.csv"
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
  ovpn_migrate_require_maintenance || return $?
  . "$LIB_DIR/upgrade.sh"
  case "$subcommand" in
  plan)
    [ "$yes" = false ] || ovpn_die '--yes is valid only with migrate apply'
    if [ -e "$(ovpn_migrate_code_transaction_file)" ] ||
      [ -e "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env" ]; then
      ovpn_schema_probe
      ovpn_migrate_print_plan "$json" \
        "${OVPN_SCHEMA_PROJECT_VERSION:-0}" "$OVPN_CURRENT_DATA_SCHEMA" \
        recovery 0 true \
        'an interrupted migration must be recovered by migrate apply'
      return 78
    fi
    if [ -n "$target_version" ]; then
      ovpn_migrate_select_target "$target_version" false || return $?
    fi
    ovpn_migrate_plan "$json" "$OVPN_MIGRATE_TARGET_VERSION"
    [ -z "$OVPN_MIGRATE_TARGET_WORK" ] || rm -rf "$OVPN_MIGRATE_TARGET_WORK"
    ;;
  apply)
    [ "$json" = false ] || ovpn_die '--json is valid only with migrate plan'
    ovpn_migrate_recover_interrupted
    ovpn_schema_probe
    if [ "$OVPN_SCHEMA_STATUS" = CURRENT ]; then
      if [ -n "$target_version" ]; then
        [ "$yes" = true ] || ovpn_migrate_confirm_apply || return $?
        ovpn_migrate_select_target "$target_version" true || return $?
        ovpn_upgrade_activate "$OVPN_MIGRATE_TARGET_VERSION" "$OVPN_MIGRATE_TARGET_DIR" ||
          return $?
        rm -rf "$OVPN_MIGRATE_TARGET_WORK"
      fi
      return 0
    fi
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
    if [ -n "$target_version" ]; then
      ovpn_migrate_select_target "$target_version" true || return $?
      # shellcheck source=/usr/local/lib/openvpn-bootstrap.sh
      . "$OVPN_BOOTSTRAP_LIB"
      OVPN_MIGRATE_OLD_ACTIVE="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/active")" ||
        OVPN_MIGRATE_OLD_ACTIVE=embedded
      OVPN_MIGRATE_OLD_PREVIOUS="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/previous")" ||
        OVPN_MIGRATE_OLD_PREVIOUS=embedded
      exec {migrate_apply_management_fd}>"$OVPN_MANAGEMENT_STORE/.management.lock"
      flock -x -w 30 "$migrate_apply_management_fd" ||
        ovpn_die 'failed to acquire management-code lock for migration'
      OVPN_MIGRATE_MANAGEMENT_LOCK_HELD=true
      ovpn_migrate_code_transaction_write
    fi
    ovpn_migration_transaction_run "$OVPN_MIGRATE_APPLY_SOURCE" \
      "$OVPN_CURRENT_DATA_SCHEMA" ovpn_migrate_apply_stage ovpn_migrate_validate_current \
      "$([ -n "$target_version" ] && printf ovpn_migrate_finalize_code)" \
      "$([ -n "$target_version" ] && printf ovpn_migrate_restore_code)"
    if [ -n "$target_version" ]; then
      rm -f "$(ovpn_migrate_code_transaction_file)"
      rm -rf "$OVPN_MIGRATE_TARGET_WORK"
      flock -u "$migrate_apply_management_fd"
    fi
    ovpn_migrate_report_profiles
    ;;
  *) ovpn_die 'usage: ovpn migrate <plan|apply>' ;;
  esac
}
