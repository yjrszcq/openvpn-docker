#!/usr/bin/env bash

OVPN_CURRENT_DATA_SCHEMA=3
OVPN_SCHEMA_STATUS=UNKNOWN
OVPN_SCHEMA_PROJECT_VERSION=''
OVPN_SCHEMA_FILE_VERSION=''

ovpn_schema_read_project_version() {
  local file="$OVPN_DATA_DIR/config/project.env"
  local line value found=''

  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      OVPN_CONFIG_VERSION=*)
        [ -z "$found" ] || return 1
        value="${line#OVPN_CONFIG_VERSION=}"
        [[ "$value" =~ ^[1-9][0-9]*$ ]] || return 1
        found="$value"
        ;;
    esac
  done <"$file"
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

ovpn_schema_read_version_file() {
  local file="$OVPN_DATA_DIR/config/schema-version"
  local value extra

  [ -r "$file" ] || return 1
  IFS= read -r value <"$file" || true
  IFS= read -r extra < <(sed -n '2p' "$file") || true
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || return 1
  [ -z "$extra" ] || return 1
  printf '%s\n' "$value"
}

ovpn_schema_read_transaction_target() {
  local file="$OVPN_DATA_DIR/.init-transaction"
  local line value found=''

  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      target_schema=*)
        [ -z "$found" ] || return 1
        value="${line#target_schema=}"
        [[ "$value" =~ ^[1-9][0-9]*$ ]] || return 1
        found="$value"
        ;;
    esac
  done <"$file"
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

ovpn_schema_probe() {
  local project_present=false schema_present=false

  OVPN_SCHEMA_STATUS=UNKNOWN
  OVPN_SCHEMA_PROJECT_VERSION=''
  OVPN_SCHEMA_FILE_VERSION=''

  if ovpn_data_dir_is_empty; then
    OVPN_SCHEMA_STATUS=EMPTY
    return 0
  fi

  if [ -e "$OVPN_DATA_DIR/config/project.env" ]; then
    project_present=true
    OVPN_SCHEMA_PROJECT_VERSION="$(ovpn_schema_read_project_version)" || {
      OVPN_SCHEMA_STATUS=INVALID
      return 0
    }
  fi
  if [ -e "$OVPN_DATA_DIR/config/schema-version" ]; then
    schema_present=true
    OVPN_SCHEMA_FILE_VERSION="$(ovpn_schema_read_version_file)" || {
      OVPN_SCHEMA_STATUS=INVALID
      return 0
    }
  fi

  if [ "$project_present" = false ] && [ "$schema_present" = false ]; then
    local transaction_target=''
    transaction_target="$(ovpn_schema_read_transaction_target)" || true
    if [ "$transaction_target" = "$OVPN_CURRENT_DATA_SCHEMA" ]; then
      OVPN_SCHEMA_STATUS=CURRENT_INCOMPLETE
    else
      OVPN_SCHEMA_STATUS=UNKNOWN
    fi
  elif [ "$project_present" = true ] && [ "$schema_present" = true ]; then
    if [ "$OVPN_SCHEMA_PROJECT_VERSION" != "$OVPN_SCHEMA_FILE_VERSION" ]; then
      OVPN_SCHEMA_STATUS=CONFLICT
    elif [ "$OVPN_SCHEMA_PROJECT_VERSION" -eq "$OVPN_CURRENT_DATA_SCHEMA" ]; then
      OVPN_SCHEMA_STATUS=CURRENT
    elif [ "$OVPN_SCHEMA_PROJECT_VERSION" -lt "$OVPN_CURRENT_DATA_SCHEMA" ]; then
      OVPN_SCHEMA_STATUS=OLD
    else
      OVPN_SCHEMA_STATUS=NEWER
    fi
  else
    local present_version
    present_version="${OVPN_SCHEMA_PROJECT_VERSION:-$OVPN_SCHEMA_FILE_VERSION}"
    if [ "$present_version" -eq "$OVPN_CURRENT_DATA_SCHEMA" ]; then
      OVPN_SCHEMA_STATUS=CURRENT_INCOMPLETE
    elif [ "$present_version" -lt "$OVPN_CURRENT_DATA_SCHEMA" ]; then
      OVPN_SCHEMA_STATUS=OLD
    else
      OVPN_SCHEMA_STATUS=NEWER
    fi
  fi
}

ovpn_schema_initialization_in_progress() {
  local entry

  [ -e "$OVPN_DATA_DIR/.init-transaction" ] && return 0
  for entry in "$OVPN_DATA_DIR"/.staging-init-*; do
    [ -e "$entry" ] && return 0
  done
  return 1
}

ovpn_schema_command_uses_data() {
  local command="$1"
  local argument
  shift

  for argument in "$@"; do
    case "$argument" in -h|--help) return 1 ;; esac
  done
  case "$command" in
    help|-h|--help|-v|--version|migrate) return 1 ;;
    upgrade) return 0 ;;
    runtime)
      case "${1:-}" in
        version|capabilities) return 1 ;;
        *) return 0 ;;
      esac
      ;;
    init|start|config|client|network|repair|state|render) return 0 ;;
    *) return 1 ;;
  esac
}

ovpn_schema_gate_command() {
  local command="$1"
  shift

  ovpn_schema_command_uses_data "$command" "$@" || return 0
  ovpn_schema_probe
  if [ "$OVPN_SCHEMA_STATUS" = UNKNOWN ] && ovpn_schema_initialization_in_progress; then
    ovpn_with_data_lock init true
    ovpn_schema_probe
  fi
  case "$OVPN_SCHEMA_STATUS" in
    EMPTY|CURRENT) return 0 ;;
    CURRENT_INCOMPLETE)
      case "$command" in
        start|state|repair) return 0 ;;
      esac
      ovpn_log "data schema $OVPN_CURRENT_DATA_SCHEMA metadata is incomplete; run 'ovpn state doctor'"
      return 78
      ;;
    OLD)
      ovpn_log "data schema migration required; run 'docker compose run --rm openvpn-maintenance migrate plan'"
      return 78
      ;;
    NEWER)
      ovpn_log "data schema is newer than this image supports ($OVPN_CURRENT_DATA_SCHEMA)"
      return 78
      ;;
    CONFLICT)
      ovpn_log 'data schema metadata conflicts; refusing to access persistent state'
      return 78
      ;;
    INVALID|UNKNOWN)
      ovpn_log 'data schema cannot be determined; refusing to access persistent state'
      return 78
      ;;
  esac
}
