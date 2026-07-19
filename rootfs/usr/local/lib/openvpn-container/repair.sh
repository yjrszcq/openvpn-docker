#!/usr/bin/env bash

OVPN_REPAIR_ACTION_IDS=()
OVPN_REPAIR_ACTION_TARGETS=()
OVPN_REPAIR_ACTION_KINDS=()
OVPN_REPAIR_BLOCKED_IDS=()
OVPN_REPAIR_BLOCKED_SEVERITIES=()
OVPN_REPAIR_BLOCKED_ACTIONS=()

ovpn_repair_plan_reset() {
  OVPN_REPAIR_ACTION_IDS=()
  OVPN_REPAIR_ACTION_TARGETS=()
  OVPN_REPAIR_ACTION_KINDS=()
  OVPN_REPAIR_BLOCKED_IDS=()
  OVPN_REPAIR_BLOCKED_SEVERITIES=()
  OVPN_REPAIR_BLOCKED_ACTIONS=()
}

ovpn_repair_plan_add_action() {
  OVPN_REPAIR_ACTION_IDS+=("$1")
  OVPN_REPAIR_ACTION_TARGETS+=("$2")
  OVPN_REPAIR_ACTION_KINDS+=("${3:-safe}")
}

ovpn_repair_plan_add_blocked() {
  OVPN_REPAIR_BLOCKED_IDS+=("$1")
  OVPN_REPAIR_BLOCKED_SEVERITIES+=("$2")
  OVPN_REPAIR_BLOCKED_ACTIONS+=("$3")
}

ovpn_repair_plan_add_issue() {
  local id="$1"
  local severity="$2"
  local action="$3"
  local client_name client_id

  case "$severity" in
    repairable)
      case "$id" in
        SCHEMA_VERSION_MISSING)
          ovpn_repair_plan_add_action WRITE_SCHEMA_VERSION config/schema-version
          ;;
        INSTANCE_METADATA_MISSING)
          ovpn_repair_plan_add_action REBUILD_METADATA meta/instance.json
          ;;
        SERVER_CONFIG_MISSING)
          ovpn_repair_plan_add_action RENDER_SERVER_CONFIG server/server.conf
          ;;
        CRL_MISSING|CRL_INVALID|CRL_CA_MISMATCH|CRL_EXPIRED)
          ovpn_repair_plan_add_action REGENERATE_CRL pki/crl.pem
          ;;
        CLIENT_PROFILE_MISSING_*)
          client_name="${id#CLIENT_PROFILE_MISSING_}"
          ovpn_repair_plan_add_action RENDER_CLIENT_PROFILE "clients/active/$client_name.ovpn"
          ;;
        CLIENT_IP_CCD_OUT_OF_SYNC)
          ovpn_repair_plan_add_action SYNCHRONIZE_CLIENT_IP_CCD ccd
          ;;
        CLIENT_IP_REGISTRY_NOT_CANONICAL)
          ovpn_repair_plan_add_action NORMALIZE_CLIENT_IP_DRAFT data/client-ip.csv
          ovpn_repair_plan_add_action NORMALIZE_CLIENT_IP_APPLIED meta/client-ip.applied.csv
          ;;
        *)
          ovpn_repair_plan_add_blocked "$id" "$severity" "$action"
          ;;
      esac
      ;;
    recoverable)
      case "$id" in
        CLIENT_IDENTITY_REGISTRY_RECOVERABLE)
          ovpn_repair_plan_add_action RECOVER_CLIENT_IDENTITY_REGISTRY meta/client-state.csv recover
          ovpn_repair_plan_add_action RECOVER_CLIENT_IP_DRAFT data/client-ip.csv recover
          ovpn_repair_plan_add_action RECOVER_CLIENT_IP_APPLIED meta/client-ip.applied.csv recover
          ovpn_repair_plan_add_action RECOVER_CLIENT_PROFILES clients recover
          ;;
        CA_CERT_MISSING)
          ovpn_repair_plan_add_action RECOVER_CA_CERT pki/ca.crt recover
          ;;
        TLS_CRYPT_KEY_MISSING)
          ovpn_repair_plan_add_action RECOVER_TLS_CRYPT_KEY secrets/tls-crypt.key recover
          ;;
        CLIENT_CERT_MISSING_*)
          client_name="${id#CLIENT_CERT_MISSING_}"
          client_id="$(ovpn_registry_current_id_by_name "$client_name")" || {
            ovpn_repair_plan_add_blocked "$id" "$severity" RESTORE_CLIENT_IP_REGISTRY
            return 0
          }
          ovpn_repair_plan_add_action RECOVER_CLIENT_CERT "pki/issued/$client_id.crt" recover
          ;;
        CLIENT_KEY_MISSING_*)
          client_name="${id#CLIENT_KEY_MISSING_}"
          client_id="$(ovpn_registry_current_id_by_name "$client_name")" || {
            ovpn_repair_plan_add_blocked "$id" "$severity" RESTORE_CLIENT_IP_REGISTRY
            return 0
          }
          ovpn_repair_plan_add_action RECOVER_CLIENT_KEY "pki/private/$client_id.key" recover
          ;;
        *)
          ovpn_repair_plan_add_blocked "$id" "$severity" "$action"
          ;;
      esac
      ;;
    *)
      ovpn_repair_plan_add_blocked "$id" "$severity" "$action"
      ;;
  esac
}

ovpn_repair_plan_build() {
  local index

  ovpn_repair_plan_reset
  ovpn_state_scan
  for ((index = 0; index < ${#OVPN_STATE_ISSUE_IDS[@]}; index++)); do
    ovpn_repair_plan_add_issue \
      "${OVPN_STATE_ISSUE_IDS[index]}" \
      "${OVPN_STATE_ISSUE_SEVERITIES[index]}" \
      "${OVPN_STATE_ISSUE_ACTIONS[index]}"
  done

  if [ "$OVPN_STATE" != EMPTY ] && [ ! -d "$OVPN_RUNTIME_DIR" ]; then
    ovpn_repair_plan_add_action ENSURE_RUNTIME_DIRECTORY "$OVPN_RUNTIME_DIR"
  fi
}

ovpn_repair_plan_print_json() {
  local index

  printf '{\n'
  printf '  "state": '
  ovpn_state_print_json_string "$OVPN_STATE"
  printf ',\n  "actions": [\n'
  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    printf '    {"id": '
    ovpn_state_print_json_string "${OVPN_REPAIR_ACTION_IDS[index]}"
    printf ', "kind": '
    ovpn_state_print_json_string "${OVPN_REPAIR_ACTION_KINDS[index]}"
    printf ', "target": '
    ovpn_state_print_json_string "${OVPN_REPAIR_ACTION_TARGETS[index]}"
    printf '}'
    if [ "$index" -lt $((${#OVPN_REPAIR_ACTION_IDS[@]} - 1)) ]; then
      printf ','
    fi
    printf '\n'
  done
  printf '  ],\n  "blocked": [\n'
  for ((index = 0; index < ${#OVPN_REPAIR_BLOCKED_IDS[@]}; index++)); do
    printf '    {"id": '
    ovpn_state_print_json_string "${OVPN_REPAIR_BLOCKED_IDS[index]}"
    printf ', "severity": '
    ovpn_state_print_json_string "${OVPN_REPAIR_BLOCKED_SEVERITIES[index]}"
    printf ', "recommended_action": '
    ovpn_state_print_json_string "${OVPN_REPAIR_BLOCKED_ACTIONS[index]}"
    printf '}'
    if [ "$index" -lt $((${#OVPN_REPAIR_BLOCKED_IDS[@]} - 1)) ]; then
      printf ','
    fi
    printf '\n'
  done
  printf '  ]\n}\n'
}

ovpn_repair_plan_print() {
  local index kind

  printf 'Instance: %s\n' "$OVPN_STATE"
  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    case "${OVPN_REPAIR_ACTION_KINDS[index]}" in
      safe) kind=SAFE ;;
      recover) kind=RECOVER ;;
      *) kind=UNKNOWN ;;
    esac
    printf '\n[%s] %s\n' "$kind" "${OVPN_REPAIR_ACTION_IDS[index]}"
    printf 'Target: %s\n' "${OVPN_REPAIR_ACTION_TARGETS[index]}"
  done
  for ((index = 0; index < ${#OVPN_REPAIR_BLOCKED_IDS[@]}; index++)); do
    printf '\n[BLOCKED] %s\n' "${OVPN_REPAIR_BLOCKED_IDS[index]}"
    printf 'Severity: %s\n' "${OVPN_REPAIR_BLOCKED_SEVERITIES[index]}"
    printf 'Recommended action: %s\n' "${OVPN_REPAIR_BLOCKED_ACTIONS[index]}"
  done
  printf '\nResult: %s automatic actions available\n' "${#OVPN_REPAIR_ACTION_IDS[@]}"
}

ovpn_repair_plan_command() {
  local output_format=plain

  case "$#" in
    0) ;;
    1) [ "$1" = --json ] && output_format=json || {
      ovpn_log 'usage: ovpn repair plan [--json]'
      exit 64
    } ;;
    *) ovpn_log 'usage: ovpn repair plan [--json]'; exit 64 ;;
  esac

  ovpn_repair_plan_build
  if [ "$output_format" = json ]; then
    ovpn_repair_plan_print_json
  else
    ovpn_repair_plan_print
  fi

  case "$OVPN_STATE" in
    CRITICAL|UNRECOVERABLE)
      ovpn_exit_for_state "$OVPN_STATE"
      ;;
  esac
}

OVPN_REPAIR_TRANSACTION_ID=''
OVPN_REPAIR_STAGE_DIR=''
OVPN_REPAIR_SNAPSHOT_DIR=''
OVPN_REPAIR_JOURNAL_PATH=''
OVPN_REPAIR_BEFORE_STATE=''
OVPN_REPAIR_TRANSACTION_SUCCESS=false
OVPN_REPAIR_RUNTIME_WAS_PRESENT=false

ovpn_repair_target_is_persistent() {
  case "$1" in
    /*) return 1 ;;
    *) return 0 ;;
  esac
}

ovpn_repair_checksum() {
  local path="$1"
  local checksum

  if [ ! -e "$path" ]; then
    printf 'missing\n'
    return 0
  fi
  if checksum="$(sha256sum "$path" 2>/dev/null)"; then
    printf '%s\n' "${checksum%% *}"
    return 0
  fi
  printf 'unavailable\n'
}

ovpn_repair_transaction_start() {
  local repair_root

  repair_root="$OVPN_DATA_DIR/repair"
  OVPN_REPAIR_TRANSACTION_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$-$(ovpn_instance_id)"
  if [ -d "$OVPN_RUNTIME_DIR" ]; then
    OVPN_REPAIR_RUNTIME_WAS_PRESENT=true
  else
    OVPN_REPAIR_RUNTIME_WAS_PRESENT=false
  fi
  OVPN_REPAIR_STAGE_DIR="$repair_root/.stage-$OVPN_REPAIR_TRANSACTION_ID"
  OVPN_REPAIR_SNAPSHOT_DIR="$repair_root/snapshots/$OVPN_REPAIR_TRANSACTION_ID"
  OVPN_REPAIR_JOURNAL_PATH="$repair_root/journal/$OVPN_REPAIR_TRANSACTION_ID.json"
  mkdir -p "$OVPN_REPAIR_STAGE_DIR" "$OVPN_REPAIR_SNAPSHOT_DIR" "$(dirname "$OVPN_REPAIR_JOURNAL_PATH")"
  chmod 700 "$repair_root" "$OVPN_REPAIR_STAGE_DIR" "$OVPN_REPAIR_SNAPSHOT_DIR" "$(dirname "$OVPN_REPAIR_JOURNAL_PATH")"
}

ovpn_repair_snapshot_actions() {
  local index target source snapshot_path
  local manifest="$OVPN_REPAIR_SNAPSHOT_DIR/manifest.tsv"

  : >"$manifest"
  chmod 600 "$manifest"
  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    target="${OVPN_REPAIR_ACTION_TARGETS[index]}"
    ovpn_repair_target_is_persistent "$target" || continue
    source="$OVPN_DATA_DIR/$target"
    if [ -e "$source" ]; then
      snapshot_path="$OVPN_REPAIR_SNAPSHOT_DIR/$target"
      mkdir -p "$(dirname "$snapshot_path")" || ovpn_die "failed to create snapshot directory for $target"
      cp -a "$source" "$snapshot_path" || ovpn_die "failed to snapshot $target"
      printf 'present\t%s\n' "$target" >>"$manifest"
    else
      printf 'missing\t%s\n' "$target" >>"$manifest"
    fi
  done
}

ovpn_repair_stage_schema_version() {
  local output_path="$OVPN_REPAIR_STAGE_DIR/config/schema-version"

  ovpn_config_load
  mkdir -p "$(dirname "$output_path")"
  printf '%s\n' "$OVPN_CONFIG_VERSION" >"$output_path"
  chmod 600 "$output_path"
}

ovpn_repair_stage_crl() {
  rm -rf "$OVPN_REPAIR_STAGE_DIR/pki"
  cp -a "$OVPN_DATA_DIR/pki" "$OVPN_REPAIR_STAGE_DIR/pki"
  (
    OVPN_DATA_DIR="$OVPN_REPAIR_STAGE_DIR"
    ovpn_pki_generate_crl
  )
}

ovpn_repair_stage_action() {
  local id="$1"
  local target="$2"
  local client_id

  case "$id" in
    WRITE_SCHEMA_VERSION)
      ovpn_repair_stage_schema_version
      ;;
    REBUILD_METADATA)
      ovpn_metadata_write_to "$OVPN_REPAIR_STAGE_DIR/$target" "$OVPN_DATA_DIR"
      ;;
    RENDER_SERVER_CONFIG)
      ovpn_render_server --output "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    REGENERATE_CRL)
      ovpn_repair_stage_crl
      ;;
    RENDER_CLIENT_PROFILE)
      client_name="${target#clients/active/}"
      client_name="${client_name%.ovpn}"
      ovpn_render_client "$client_name" --output "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CA_CERT)
      ovpn_recovery_stage_ca_cert "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_TLS_CRYPT_KEY)
      ovpn_recovery_stage_tls_crypt_key "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CLIENT_IDENTITY_REGISTRY)
      ovpn_recovery_stage_client_registry "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CLIENT_IP_DRAFT)
      ovpn_recovery_stage_client_ip_registry \
        "$(ovpn_registry_client_ip_file)" "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CLIENT_IP_APPLIED)
      ovpn_recovery_stage_client_ip_registry \
        "$(ovpn_registry_applied_file)" "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CLIENT_PROFILES)
      ovpn_recovery_stage_client_profiles "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CLIENT_CERT)
      client_id="${target#pki/issued/}"
      client_id="${client_id%.crt}"
      ovpn_recovery_stage_client_certificate "$client_id" "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    RECOVER_CLIENT_KEY)
      client_id="${target#pki/private/}"
      client_id="${client_id%.key}"
      ovpn_recovery_stage_client_key "$client_id" "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    SYNCHRONIZE_CLIENT_IP_CCD)
      ovpn_state_ipam_stage_ccd "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    NORMALIZE_CLIENT_IP_DRAFT|NORMALIZE_CLIENT_IP_APPLIED)
      ovpn_state_ipam_stage_canonical_registry "$OVPN_REPAIR_STAGE_DIR/$target"
      ;;
    ENSURE_RUNTIME_DIRECTORY)
      ;;
    *)
      ovpn_die "unsupported automatic repair action: $id"
      ;;
  esac
}

ovpn_repair_stage_actions() {
  local index

  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    ovpn_repair_stage_action \
      "${OVPN_REPAIR_ACTION_IDS[index]}" \
      "${OVPN_REPAIR_ACTION_TARGETS[index]}"
  done
}

ovpn_repair_validate_stage() {
  local validation_dir="$OVPN_REPAIR_STAGE_DIR/validation"
  local entry index target source validation_state
  local saved_data_dir="$OVPN_DATA_DIR"
  local saved_config_dir="$OVPN_CONFIG_DIR"
  local saved_project_env="$OVPN_PROJECT_ENV"
  local saved_schema_version_file="$OVPN_SCHEMA_VERSION_FILE"

  mkdir -p "$validation_dir"
  for entry in ccd clients config data meta pki secrets server; do
    if [ -e "$OVPN_DATA_DIR/$entry" ]; then
      cp -a "$OVPN_DATA_DIR/$entry" "$validation_dir/$entry" || ovpn_die "failed to stage validation copy of $entry"
    fi
  done
  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    target="${OVPN_REPAIR_ACTION_TARGETS[index]}"
    ovpn_repair_target_is_persistent "$target" || continue
    source="$OVPN_REPAIR_STAGE_DIR/$target"
    [ -e "$source" ] || ovpn_die "missing staged repair target: $target"
    mkdir -p "$(dirname "$validation_dir/$target")"
    rm -rf "${validation_dir:?}/$target"
    cp -a "$source" "$validation_dir/$target" || ovpn_die "failed to stage validation copy of $target"
  done

  validation_state="$(
    OVPN_DATA_DIR="$validation_dir"
    OVPN_CONFIG_DIR="$validation_dir/config"
    OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
    OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
    ovpn_state_scan
    printf '%s' "$OVPN_STATE"
  )" || ovpn_die "failed to validate staged repair actions"

  [ "$validation_state" = HEALTHY ] || ovpn_die "staged automatic repair is not healthy: $validation_state"
}

ovpn_repair_install_staged_actions() {
  local index id target source destination

  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    id="${OVPN_REPAIR_ACTION_IDS[index]}"
    target="${OVPN_REPAIR_ACTION_TARGETS[index]}"
    if ! ovpn_repair_target_is_persistent "$target"; then
      mkdir -p "$OVPN_RUNTIME_DIR"
      chmod 750 "$OVPN_RUNTIME_DIR"
    else
      source="$OVPN_REPAIR_STAGE_DIR/$target"
      destination="$OVPN_DATA_DIR/$target"
      mkdir -p "$(dirname "$destination")"
      rm -rf "$destination"
      mv "$source" "$destination" || ovpn_die "failed to install repaired file: $target"
    fi
    if [ "${OVPN_REPAIR_FAIL_AFTER_INSTALL:-}" = "$id" ]; then
      ovpn_die "injected repair failure after $id"
    fi
  done
}

ovpn_repair_rollback() {
  local status target destination snapshot_path
  local manifest="$OVPN_REPAIR_SNAPSHOT_DIR/manifest.tsv"

  [ -r "$manifest" ] || return 0
  while IFS=$'\t' read -r status target; do
    [ -n "$target" ] || continue
    destination="$OVPN_DATA_DIR/$target"
    case "$status" in
      present)
        snapshot_path="$OVPN_REPAIR_SNAPSHOT_DIR/$target"
        mkdir -p "$(dirname "$destination")"
        rm -rf "$destination"
        cp -a "$snapshot_path" "$destination"
        ;;
      missing)
        rm -rf "$destination"
        ;;
    esac
  done <"$manifest"
  if [ "$OVPN_REPAIR_RUNTIME_WAS_PRESENT" = false ]; then
    rm -rf "$OVPN_RUNTIME_DIR"
  fi
}

ovpn_repair_write_journal() {
  local result="$1"
  local index target before_path before_checksum after_checksum
  local first=true
  local temporary_path="${OVPN_REPAIR_JOURNAL_PATH}.tmp"

  umask 077
  {
    printf '{\n  "transaction_id": '
    ovpn_state_print_json_string "$OVPN_REPAIR_TRANSACTION_ID"
    printf ',\n  "before_state": '
    ovpn_state_print_json_string "$OVPN_REPAIR_BEFORE_STATE"
    printf ',\n  "result": '
    ovpn_state_print_json_string "$result"
    printf ',\n  "actions": [\n'
    for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
      target="${OVPN_REPAIR_ACTION_TARGETS[index]}"
      ovpn_repair_target_is_persistent "$target" || continue
      if [ "$first" = false ]; then
        printf ',\n'
      fi
      first=false
      before_path="$OVPN_REPAIR_SNAPSHOT_DIR/$target"
      before_checksum="$(ovpn_repair_checksum "$before_path")"
      after_checksum="$(ovpn_repair_checksum "$OVPN_DATA_DIR/$target")"
      printf '    {"id": '
      ovpn_state_print_json_string "${OVPN_REPAIR_ACTION_IDS[index]}"
      printf ', "target": '
      ovpn_state_print_json_string "$target"
      printf ', "before_sha256": '
      ovpn_state_print_json_string "$before_checksum"
      printf ', "after_sha256": '
      ovpn_state_print_json_string "$after_checksum"
      printf '}'
    done
    printf '\n  ]\n}\n'
  } >"$temporary_path"
  mv "$temporary_path" "$OVPN_REPAIR_JOURNAL_PATH"
  chmod 600 "$OVPN_REPAIR_JOURNAL_PATH"
}

ovpn_repair_transaction_cleanup() {
  local status=$?

  if [ "$OVPN_REPAIR_TRANSACTION_SUCCESS" != true ]; then
    ovpn_repair_rollback
    ovpn_repair_write_journal failed || true
  fi
  [ -z "$OVPN_REPAIR_STAGE_DIR" ] || rm -rf "$OVPN_REPAIR_STAGE_DIR"
  return "$status"
}

ovpn_repair_apply_inner() {
  ovpn_repair_plan_build
  case "$OVPN_STATE" in
    HEALTHY|DEGRADED_REPAIRABLE|DEGRADED_RECOVERABLE)
      ;;
    *)
      ovpn_log "instance state is $OVPN_STATE; refusing automatic repair"
      ovpn_repair_plan_print >&2
      exit 78
      ;;
  esac
  if [ "${#OVPN_REPAIR_ACTION_IDS[@]}" -eq 0 ]; then
    ovpn_log 'no automatic repair actions are required'
    return 0
  fi

  OVPN_REPAIR_BEFORE_STATE="$OVPN_STATE"
  OVPN_REPAIR_TRANSACTION_SUCCESS=false
  OVPN_REPAIR_RUNTIME_WAS_PRESENT=false
  trap ovpn_repair_transaction_cleanup EXIT
  ovpn_repair_transaction_start
  ovpn_repair_snapshot_actions
  ovpn_repair_stage_actions
  ovpn_repair_validate_stage
  ovpn_repair_install_staged_actions
  ovpn_state_scan
  [ "$OVPN_STATE" = HEALTHY ] || ovpn_die "automatic repair did not restore a healthy instance: $OVPN_STATE"
  ovpn_repair_write_journal success
  OVPN_REPAIR_TRANSACTION_SUCCESS=true
  rm -rf "$OVPN_REPAIR_STAGE_DIR"
  trap - EXIT
  ovpn_log "completed ${#OVPN_REPAIR_ACTION_IDS[@]} automatic repair actions"
}


ovpn_repair_command() {
  local operation="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_repair_usage
    return 0
  fi
  [ -n "$operation" ] || ovpn_die "usage: ovpn repair <plan|apply> [--json]"
  shift
  case "$operation" in
    plan)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn repair plan [--json]" "Inspect eligible repair actions without changing state."
      else
        ovpn_repair_plan_command "$@"
      fi
      ;;
    apply)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn repair apply" "Apply eligible repair actions under the data lock."
      else
        [ "$#" -eq 0 ] || {
          ovpn_log "usage: ovpn repair apply"
          exit 64
        }
        ovpn_with_data_lock repair ovpn_repair_apply_inner
      fi
      ;;
    *) ovpn_die "usage: ovpn repair <plan|apply> [--json]" ;;
  esac
}
