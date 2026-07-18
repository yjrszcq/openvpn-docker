#!/usr/bin/env bash

OVPN_MIGRATION_TRANSACTION_ROOT="${OVPN_MIGRATION_TRANSACTION_ROOT:-$OVPN_DATA_DIR/repair/migrations}"
OVPN_MIGRATION_TRANSACTION_ID=''
OVPN_MIGRATION_TRANSACTION_STAGE=''
OVPN_MIGRATION_TRANSACTION_SNAPSHOT=''
OVPN_MIGRATION_TRANSACTION_SNAPSHOT_SHA256=''
OVPN_MIGRATION_TRANSACTION_REPORT=''
OVPN_MIGRATION_TRANSACTION_SUCCESS=false

ovpn_migration_business_entries() {
  printf '%s\n' ccd clients config data meta pki secrets server
}

ovpn_migration_transaction_write_marker() {
  local source_schema="$1"
  local target_schema="$2"
  local temporary="$OVPN_MIGRATION_TRANSACTION_ROOT/.transaction.$$"

  umask 077
  {
    printf 'FORMAT_VERSION=1\n'
    printf 'TRANSACTION_ID=%s\n' "$OVPN_MIGRATION_TRANSACTION_ID"
    printf 'SOURCE_SCHEMA=%s\n' "$source_schema"
    printf 'TARGET_SCHEMA=%s\n' "$target_schema"
    printf 'SNAPSHOT=%s\n' "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT"
    printf 'SNAPSHOT_SHA256=%s\n' "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT_SHA256"
  } >"$temporary"
  mv "$temporary" "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env"
  chmod 600 "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env"
}

ovpn_migration_transaction_marker_value() {
  local key="$1"
  local file="$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env"
  local line found=''

  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      "$key="*)
        [ -z "$found" ] || return 1
        found="${line#*=}"
        ;;
    esac
  done <"$file"
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

ovpn_migration_transaction_restore() {
  local snapshot="$1"
  local entry

  [ -r "$snapshot" ] || ovpn_die "migration snapshot is unavailable: $snapshot"
  while IFS= read -r entry; do
    rm -rf "$OVPN_DATA_DIR/$entry"
  done < <(ovpn_migration_business_entries)
  tar -C "$OVPN_DATA_DIR" -xzf "$snapshot" ||
    ovpn_die 'failed to restore the migration snapshot'
}

ovpn_migration_transaction_recover_interrupted() {
  local marker="$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env"
  local format transaction_id source_schema target_schema
  local snapshot snapshot_sha256 actual_sha256 expected_prefix relative

  [ -e "$marker" ] || return 0
  format="$(ovpn_migration_transaction_marker_value FORMAT_VERSION)" ||
    ovpn_die 'migration transaction marker has no format version; manual recovery is required'
  [ "$format" = 1 ] ||
    ovpn_die 'migration transaction marker format is unsupported; manual recovery is required'
  transaction_id="$(ovpn_migration_transaction_marker_value TRANSACTION_ID)" ||
    ovpn_die 'migration transaction marker has no transaction ID; manual recovery is required'
  case "$transaction_id" in
    ''|*/*|*[!A-Za-z0-9._-]*|.*)
      ovpn_die 'migration transaction ID is invalid; manual recovery is required'
      ;;
  esac
  source_schema="$(ovpn_migration_transaction_marker_value SOURCE_SCHEMA)" ||
    ovpn_die 'migration transaction marker has no source schema; manual recovery is required'
  target_schema="$(ovpn_migration_transaction_marker_value TARGET_SCHEMA)" ||
    ovpn_die 'migration transaction marker has no target schema; manual recovery is required'
  [[ "$source_schema" =~ ^[1-9][0-9]*$ ]] && [[ "$target_schema" =~ ^[1-9][0-9]*$ ]] ||
    ovpn_die 'migration transaction schema metadata is invalid; manual recovery is required'
  snapshot="$(ovpn_migration_transaction_marker_value SNAPSHOT)" ||
    ovpn_die 'migration transaction marker is invalid; manual recovery is required'
  snapshot_sha256="$(ovpn_migration_transaction_marker_value SNAPSHOT_SHA256)" ||
    ovpn_die 'migration transaction marker has no snapshot checksum; manual recovery is required'
  expected_prefix="$OVPN_MIGRATION_TRANSACTION_ROOT/snapshots/"
  case "$snapshot" in
    "$expected_prefix"*) ;;
    *) ovpn_die 'migration transaction snapshot path is invalid; manual recovery is required' ;;
  esac
  relative="${snapshot#"$expected_prefix"}"
  case "$relative" in
    ''|*/*|*[!A-Za-z0-9._-]*|.*)
      ovpn_die 'migration transaction snapshot name is invalid; manual recovery is required'
      ;;
    *.tar.gz) ;;
    *) ovpn_die 'migration transaction snapshot name is invalid; manual recovery is required' ;;
  esac
  [[ "$snapshot_sha256" =~ ^[0-9a-f]{64}$ ]] ||
    ovpn_die 'migration transaction snapshot checksum is invalid; manual recovery is required'
  actual_sha256="$(sha256sum "$snapshot")" ||
    ovpn_die 'migration transaction snapshot cannot be read; manual recovery is required'
  actual_sha256="${actual_sha256%% *}"
  [ "$actual_sha256" = "$snapshot_sha256" ] ||
    ovpn_die 'migration transaction snapshot checksum mismatch; manual recovery is required'
  ovpn_log 'recovering an interrupted migration transaction'
  ovpn_migration_transaction_restore "$snapshot"
  OVPN_MIGRATION_TRANSACTION_ID="$transaction_id"
  OVPN_MIGRATION_TRANSACTION_SNAPSHOT="$snapshot"
  OVPN_MIGRATION_TRANSACTION_SNAPSHOT_SHA256="$snapshot_sha256"
  OVPN_MIGRATION_TRANSACTION_REPORT="$OVPN_MIGRATION_TRANSACTION_ROOT/reports/$transaction_id.json"
  ovpn_migration_transaction_write_report "$source_schema" "$target_schema" recovered
  rm -rf "$OVPN_MIGRATION_TRANSACTION_ROOT/staging/$transaction_id"
  rm -f "$marker"
}

ovpn_migration_transaction_start() {
  local source_schema="$1"
  local target_schema="$2"
  local entry

  OVPN_MIGRATION_TRANSACTION_ID="$(date -u +%Y%m%dT%H%M%S%NZ)-$$-$(ovpn_instance_id)"
  OVPN_MIGRATION_TRANSACTION_STAGE="$OVPN_MIGRATION_TRANSACTION_ROOT/staging/$OVPN_MIGRATION_TRANSACTION_ID/data"
  OVPN_MIGRATION_TRANSACTION_SNAPSHOT="$OVPN_MIGRATION_TRANSACTION_ROOT/snapshots/$OVPN_MIGRATION_TRANSACTION_ID.tar.gz"
  OVPN_MIGRATION_TRANSACTION_REPORT="$OVPN_MIGRATION_TRANSACTION_ROOT/reports/$OVPN_MIGRATION_TRANSACTION_ID.json"
  mkdir -p \
    "$OVPN_MIGRATION_TRANSACTION_STAGE" \
    "$(dirname "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT")" \
    "$(dirname "$OVPN_MIGRATION_TRANSACTION_REPORT")"
  chmod 700 \
    "$OVPN_MIGRATION_TRANSACTION_ROOT" \
    "$(dirname "$OVPN_MIGRATION_TRANSACTION_STAGE")" \
    "$OVPN_MIGRATION_TRANSACTION_STAGE" \
    "$(dirname "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT")" \
    "$(dirname "$OVPN_MIGRATION_TRANSACTION_REPORT")"

  local -a snapshot_entries=()
  while IFS= read -r entry; do
    if [ -e "$OVPN_DATA_DIR/$entry" ]; then
      snapshot_entries+=("$entry")
      cp -a "$OVPN_DATA_DIR/$entry" "$OVPN_MIGRATION_TRANSACTION_STAGE/$entry"
    fi
  done < <(ovpn_migration_business_entries)
  if [ "${#snapshot_entries[@]}" -gt 0 ]; then
    tar -C "$OVPN_DATA_DIR" -czf "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT" -- "${snapshot_entries[@]}"
  else
    tar -C "$OVPN_DATA_DIR" -czf "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT" --files-from /dev/null
  fi
  chmod 600 "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT"
  OVPN_MIGRATION_TRANSACTION_SNAPSHOT_SHA256="$(sha256sum "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT")"
  OVPN_MIGRATION_TRANSACTION_SNAPSHOT_SHA256="${OVPN_MIGRATION_TRANSACTION_SNAPSHOT_SHA256%% *}"
  ovpn_migration_transaction_write_marker "$source_schema" "$target_schema"
}

ovpn_migration_transaction_write_report() {
  local source_schema="$1"
  local target_schema="$2"
  local result="$3"
  local temporary="$OVPN_MIGRATION_TRANSACTION_REPORT.tmp"

  umask 077
  printf '{"transaction_id":"%s","source_schema":%s,"target_schema":%s,"result":"%s","snapshot":"%s"}\n' \
    "$OVPN_MIGRATION_TRANSACTION_ID" "$source_schema" "$target_schema" "$result" \
    "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT" >"$temporary"
  mv "$temporary" "$OVPN_MIGRATION_TRANSACTION_REPORT"
  chmod 600 "$OVPN_MIGRATION_TRANSACTION_REPORT"
}

ovpn_migration_transaction_commit() {
  local entry

  while IFS= read -r entry; do
    rm -rf "$OVPN_DATA_DIR/$entry"
    if [ -e "$OVPN_MIGRATION_TRANSACTION_STAGE/$entry" ]; then
      mv "$OVPN_MIGRATION_TRANSACTION_STAGE/$entry" "$OVPN_DATA_DIR/$entry"
    fi
    [ "${OVPN_MIGRATION_FAIL_AFTER_COMMIT:-}" != "$entry" ] ||
      ovpn_die "injected migration failure after committing $entry"
  done < <(ovpn_migration_business_entries)
}

ovpn_migration_transaction_cleanup() {
  local status=$?
  local source_schema="$1"
  local target_schema="$2"

  if [ "$OVPN_MIGRATION_TRANSACTION_SUCCESS" != true ]; then
    ovpn_migration_transaction_restore "$OVPN_MIGRATION_TRANSACTION_SNAPSHOT" || true
    ovpn_migration_transaction_write_report "$source_schema" "$target_schema" failed || true
  fi
  rm -rf "$(dirname "$OVPN_MIGRATION_TRANSACTION_STAGE")"
  rm -f "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env"
  return "$status"
}

ovpn_migration_transaction_inner() {
  local source_schema="$1"
  local target_schema="$2"
  local migrate_callback="$3"
  local validate_callback="$4"

  ovpn_migration_transaction_recover_interrupted
  OVPN_MIGRATION_TRANSACTION_SUCCESS=false
  ovpn_migration_transaction_start "$source_schema" "$target_schema"
  trap 'ovpn_migration_transaction_cleanup "$source_schema" "$target_schema"' EXIT
  "$migrate_callback" "$OVPN_MIGRATION_TRANSACTION_STAGE"
  "$validate_callback" "$OVPN_MIGRATION_TRANSACTION_STAGE"
  ovpn_migration_transaction_commit
  "$validate_callback" "$OVPN_DATA_DIR"
  ovpn_migration_transaction_write_report "$source_schema" "$target_schema" success
  OVPN_MIGRATION_TRANSACTION_SUCCESS=true
  rm -rf "$(dirname "$OVPN_MIGRATION_TRANSACTION_STAGE")"
  rm -f "$OVPN_MIGRATION_TRANSACTION_ROOT/transaction.env"
  trap - EXIT
}

ovpn_migration_transaction_run() {
  local source_schema="$1"
  local target_schema="$2"
  local migrate_callback="$3"
  local validate_callback="$4"

  ovpn_migrate_require_maintenance || return $?
  ovpn_with_runtime_exclusive_lock \
    ovpn_with_data_lock migration \
    ovpn_migration_transaction_inner \
    "$source_schema" "$target_schema" "$migrate_callback" "$validate_callback"
}
