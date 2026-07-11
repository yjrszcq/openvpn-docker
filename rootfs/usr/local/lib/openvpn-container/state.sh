#!/usr/bin/env bash

OVPN_STATE=EMPTY
OVPN_STATE_ISSUE_IDS=()
OVPN_STATE_ISSUE_SEVERITIES=()
OVPN_STATE_ISSUE_ACTIONS=()

ovpn_empty_dir_entry_is_ignored() {
  case "$1" in
    lost+found|.DS_Store|.ovpn-init.lock)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

ovpn_data_dir_first_nonempty_entry() (
  local entry name

  if [ ! -e "$OVPN_DATA_DIR" ]; then
    return 0
  fi
  if [ ! -d "$OVPN_DATA_DIR" ] || [ ! -r "$OVPN_DATA_DIR" ] || [ ! -x "$OVPN_DATA_DIR" ]; then
    printf '%s\n' "$OVPN_DATA_DIR"
    return 0
  fi

  shopt -s dotglob nullglob
  for entry in "$OVPN_DATA_DIR"/*; do
    name="${entry##*/}"
    if ! ovpn_empty_dir_entry_is_ignored "$name"; then
      printf '%s\n' "$entry"
      return 0
    fi
  done
)

ovpn_data_dir_is_empty() {
  [ -z "$(ovpn_data_dir_first_nonempty_entry)" ]
}

ovpn_initialization_transaction_present() {
  [ -e "$OVPN_DATA_DIR/.init-transaction" ]
}

ovpn_required_files() {
  cat <<EOF
$OVPN_DATA_DIR/config/project.env
$OVPN_DATA_DIR/config/schema-version
$OVPN_DATA_DIR/meta/instance.json
$OVPN_DATA_DIR/pki/ca.crt
$OVPN_DATA_DIR/pki/private/ca.key
$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt
$OVPN_DATA_DIR/pki/private/$OVPN_SERVER_NAME.key
$OVPN_DATA_DIR/pki/index.txt
$OVPN_DATA_DIR/pki/serial
$OVPN_DATA_DIR/pki/crl.pem
$OVPN_DATA_DIR/secrets/tls-crypt.key
$OVPN_DATA_DIR/server/server.conf
EOF
}

ovpn_missing_required_files() {
  local file
  ovpn_required_files | while IFS= read -r file; do
    [ -n "$file" ] || continue
    if [ ! -e "$file" ]; then
      printf '%s\n' "$file"
    fi
  done
}

ovpn_state_reset() {
  OVPN_STATE=HEALTHY
  OVPN_STATE_ISSUE_IDS=()
  OVPN_STATE_ISSUE_SEVERITIES=()
  OVPN_STATE_ISSUE_ACTIONS=()
}

ovpn_state_rank() {
  case "$1" in
    EMPTY|HEALTHY) printf '0\n' ;;
    DEGRADED_REPAIRABLE) printf '10\n' ;;
    DEGRADED_RECOVERABLE) printf '20\n' ;;
    DEGRADED_REISSUABLE) printf '30\n' ;;
    CRITICAL) printf '40\n' ;;
    UNRECOVERABLE) printf '50\n' ;;
    *) return 1 ;;
  esac
}

ovpn_state_consider() {
  local candidate="$1"
  local current_rank candidate_rank

  current_rank="$(ovpn_state_rank "$OVPN_STATE")" || return 1
  candidate_rank="$(ovpn_state_rank "$candidate")" || return 1
  if [ "$candidate_rank" -gt "$current_rank" ]; then
    OVPN_STATE="$candidate"
  fi
}

ovpn_state_add_issue() {
  local id="$1"
  local severity="$2"
  local action="$3"

  OVPN_STATE_ISSUE_IDS+=("$id")
  OVPN_STATE_ISSUE_SEVERITIES+=("$severity")
  OVPN_STATE_ISSUE_ACTIONS+=("$action")
}

ovpn_state_has_profile_candidates() (
  local profile
  shopt -s nullglob
  for profile in "$OVPN_DATA_DIR"/clients/active/*.ovpn "$OVPN_DATA_DIR"/clients/revoked/*.ovpn "$OVPN_DATA_DIR"/clients/archive/*.ovpn; do
    [ -f "$profile" ] && return 0
  done
  return 1
)

ovpn_state_scan_client_profiles() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name profile

  [ -r "$index" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    [ "$status" = V ] || continue
    subject="${line##*$'\t'}"
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ -n "$name" ] || continue
    [ "$name" != "$OVPN_SERVER_NAME" ] || continue
    profile="$OVPN_DATA_DIR/clients/active/$name.ovpn"
    if [ ! -e "$profile" ]; then
      ovpn_state_add_issue "CLIENT_PROFILE_MISSING_$name" repairable RENDER_CLIENT_PROFILE
      ovpn_state_consider DEGRADED_REPAIRABLE
    fi
  done <"$index"
}

ovpn_state_scan() {
  ovpn_state_reset

  if ovpn_data_dir_is_empty; then
    OVPN_STATE=EMPTY
    return 0
  fi

  if ovpn_initialization_transaction_present; then
    ovpn_state_add_issue INITIALIZATION_INTERRUPTED critical MANUAL_REVIEW
    ovpn_state_consider CRITICAL
    return 0
  fi

  if [ ! -e "$OVPN_DATA_DIR/config/project.env" ]; then
    ovpn_state_add_issue PROJECT_CONFIG_MISSING critical RESTORE_PROJECT_CONFIG
    ovpn_state_consider CRITICAL
  fi
  if [ ! -e "$OVPN_DATA_DIR/config/schema-version" ]; then
    ovpn_state_add_issue SCHEMA_VERSION_MISSING repairable WRITE_SCHEMA_VERSION
    ovpn_state_consider DEGRADED_REPAIRABLE
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/index.txt" ]; then
    ovpn_state_add_issue PKI_INDEX_MISSING critical RESTORE_PKI_DATABASE
    ovpn_state_consider CRITICAL
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/serial" ]; then
    ovpn_state_add_issue PKI_SERIAL_MISSING critical RESTORE_PKI_DATABASE
    ovpn_state_consider CRITICAL
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/private/ca.key" ]; then
    ovpn_state_add_issue CA_KEY_MISSING unrecoverable RESTORE_BACKUP
    ovpn_state_consider UNRECOVERABLE
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/ca.crt" ]; then
    if ovpn_state_has_profile_candidates; then
      ovpn_state_add_issue CA_CERT_MISSING recoverable RECOVER_CA_CERT
      ovpn_state_consider DEGRADED_RECOVERABLE
    else
      ovpn_state_add_issue CA_CERT_MISSING critical RESTORE_BACKUP
      ovpn_state_consider CRITICAL
    fi
  fi
  if [ ! -e "$OVPN_DATA_DIR/secrets/tls-crypt.key" ]; then
    if ovpn_state_has_profile_candidates; then
      ovpn_state_add_issue TLS_CRYPT_KEY_MISSING recoverable RECOVER_TLS_CRYPT_KEY
      ovpn_state_consider DEGRADED_RECOVERABLE
    else
      ovpn_state_add_issue TLS_CRYPT_KEY_MISSING critical RESTORE_BACKUP
      ovpn_state_consider CRITICAL
    fi
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt" ]; then
    ovpn_state_add_issue SERVER_CERT_MISSING reissuable REISSUE_SERVER_CERT
    ovpn_state_consider DEGRADED_REISSUABLE
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/private/$OVPN_SERVER_NAME.key" ]; then
    ovpn_state_add_issue SERVER_KEY_MISSING reissuable ROTATE_SERVER_IDENTITY
    ovpn_state_consider DEGRADED_REISSUABLE
  fi
  if [ ! -e "$OVPN_DATA_DIR/pki/crl.pem" ]; then
    ovpn_state_add_issue CRL_MISSING repairable REGENERATE_CRL
    ovpn_state_consider DEGRADED_REPAIRABLE
  fi
  if [ ! -e "$OVPN_DATA_DIR/meta/instance.json" ]; then
    ovpn_state_add_issue INSTANCE_METADATA_MISSING repairable REBUILD_METADATA
    ovpn_state_consider DEGRADED_REPAIRABLE
  fi
  if [ ! -e "$OVPN_DATA_DIR/server/server.conf" ]; then
    ovpn_state_add_issue SERVER_CONFIG_MISSING repairable RENDER_SERVER_CONFIG
    ovpn_state_consider DEGRADED_REPAIRABLE
  fi

  ovpn_state_scan_client_profiles
}

ovpn_state_detect() {
  ovpn_state_scan
  printf '%s\n' "$OVPN_STATE"
}

ovpn_state_command() {
  ovpn_state_detect
}

ovpn_require_healthy_state() {
  local index

  ovpn_state_scan
  if [ "$OVPN_STATE" != HEALTHY ]; then
    ovpn_log "instance state is $OVPN_STATE"
    for ((index = 0; index < ${#OVPN_STATE_ISSUE_IDS[@]}; index++)); do
      ovpn_log "state issue: ${OVPN_STATE_ISSUE_IDS[index]} (action: ${OVPN_STATE_ISSUE_ACTIONS[index]})"
    done
    exit 1
  fi
}
