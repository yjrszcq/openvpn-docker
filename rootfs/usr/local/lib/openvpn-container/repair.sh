#!/usr/bin/env bash

OVPN_REPAIR_ACTION_IDS=()
OVPN_REPAIR_ACTION_TARGETS=()
OVPN_REPAIR_BLOCKED_IDS=()
OVPN_REPAIR_BLOCKED_SEVERITIES=()
OVPN_REPAIR_BLOCKED_ACTIONS=()

ovpn_repair_plan_reset() {
  OVPN_REPAIR_ACTION_IDS=()
  OVPN_REPAIR_ACTION_TARGETS=()
  OVPN_REPAIR_BLOCKED_IDS=()
  OVPN_REPAIR_BLOCKED_SEVERITIES=()
  OVPN_REPAIR_BLOCKED_ACTIONS=()
}

ovpn_repair_plan_add_action() {
  OVPN_REPAIR_ACTION_IDS+=("$1")
  OVPN_REPAIR_ACTION_TARGETS+=("$2")
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
  local client_name

  if [ "$severity" != repairable ]; then
    ovpn_repair_plan_add_blocked "$id" "$severity" "$action"
    return 0
  fi

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
    printf ', "kind": "safe", "target": '
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
  local index

  printf 'Instance: %s\n' "$OVPN_STATE"
  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    printf '\n[SAFE] %s\n' "${OVPN_REPAIR_ACTION_IDS[index]}"
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
    1)
      [ "$1" = --plan ] || {
        ovpn_log 'usage: ovpn repair --plan [--json]'
        exit 64
      }
      ;;
    2)
      if [ "$1" = --plan ] && [ "$2" = --json ]; then
        output_format=json
      else
        ovpn_log 'usage: ovpn repair --plan [--json]'
        exit 64
      fi
      ;;
    *)
      ovpn_log 'usage: ovpn repair --plan [--json]'
      exit 64
      ;;
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

ovpn_repair_write_schema_version() {
  ovpn_config_load
  printf '%s\n' "$OVPN_CONFIG_VERSION" >"$OVPN_SCHEMA_VERSION_FILE.tmp"
  mv "$OVPN_SCHEMA_VERSION_FILE.tmp" "$OVPN_SCHEMA_VERSION_FILE"
  chmod 600 "$OVPN_SCHEMA_VERSION_FILE"
}

ovpn_repair_apply_action() {
  local id="$1"
  local target="$2"
  local client_name

  case "$id" in
    WRITE_SCHEMA_VERSION)
      ovpn_repair_write_schema_version
      ;;
    REBUILD_METADATA)
      ovpn_metadata_write
      ;;
    RENDER_SERVER_CONFIG)
      ovpn_render_server --output "$OVPN_DATA_DIR/server/server.conf"
      ;;
    REGENERATE_CRL)
      ovpn_pki_generate_crl
      ;;
    RENDER_CLIENT_PROFILE)
      client_name="${target#clients/active/}"
      client_name="${client_name%.ovpn}"
      ovpn_render_client "$client_name" --output "$OVPN_DATA_DIR/$target"
      ;;
    ENSURE_RUNTIME_DIRECTORY)
      mkdir -p "$OVPN_RUNTIME_DIR"
      chmod 750 "$OVPN_RUNTIME_DIR"
      ;;
    *)
      ovpn_die "unsupported safe repair action: $id"
      ;;
  esac
}

ovpn_repair_apply_inner() {
  local index

  ovpn_repair_plan_build
  case "$OVPN_STATE" in
    HEALTHY|DEGRADED_REPAIRABLE)
      ;;
    *)
      ovpn_log "instance state is $OVPN_STATE; refusing safe repair"
      ovpn_repair_plan_print >&2
      exit 78
      ;;
  esac

  for ((index = 0; index < ${#OVPN_REPAIR_ACTION_IDS[@]}; index++)); do
    ovpn_repair_apply_action \
      "${OVPN_REPAIR_ACTION_IDS[index]}" \
      "${OVPN_REPAIR_ACTION_TARGETS[index]}"
  done

  ovpn_state_scan
  if [ "$OVPN_STATE" != HEALTHY ]; then
    ovpn_log "safe repair did not restore a healthy instance; state is $OVPN_STATE"
    ovpn_exit_for_state "$OVPN_STATE"
  fi
  ovpn_log "completed ${#OVPN_REPAIR_ACTION_IDS[@]} safe repair actions"
}

ovpn_repair_command() {
  if [ "$#" -gt 0 ]; then
    ovpn_repair_plan_command "$@"
    return 0
  fi
  ovpn_with_data_lock repair ovpn_repair_apply_inner
}
