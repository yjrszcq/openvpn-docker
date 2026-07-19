#!/usr/bin/env bash

OVPN_STATE=EMPTY
OVPN_STATE_ISSUE_IDS=()
OVPN_STATE_ISSUE_SEVERITIES=()
OVPN_STATE_ISSUE_ACTIONS=()
OVPN_STATE_CLIENT_REGISTRY_RECOVERY_PENDING=false

ovpn_empty_dir_entry_is_ignored() {
  case "$1" in
    lost+found|.DS_Store|.ovpn-init.lock|.ovpn-data.lock|.ovpn-runtime.lock)
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
$OVPN_DATA_DIR/meta/client-ip.csv
$OVPN_DATA_DIR/meta/client-state.csv
$OVPN_DATA_DIR/meta/audit.jsonl
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
  while IFS= read -r file; do
    [ -n "$file" ] || continue
    if [ ! -e "$file" ]; then
      printf '%s\n' "$file"
    fi
  done < <(ovpn_required_files)
}

ovpn_state_reset() {
  OVPN_STATE=HEALTHY
  OVPN_STATE_ISSUE_IDS=()
  OVPN_STATE_ISSUE_SEVERITIES=()
  OVPN_STATE_ISSUE_ACTIONS=()
  OVPN_STATE_CLIENT_REGISTRY_RECOVERY_PENDING=false
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


ovpn_state_scan_client_profiles() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject id name profile serial

  [ -r "$index" ] || return 0
  if ! ovpn_registry_load_identities; then
    if ovpn_recovery_assess_client_registry; then
      OVPN_STATE_CLIENT_REGISTRY_RECOVERY_PENDING=true
      ovpn_state_add_recoverable_issue CLIENT_IDENTITY_REGISTRY_RECOVERABLE RECOVER_CLIENT_IDENTITY_REGISTRY
    else
      case "$OVPN_RECOVERY_STATUS" in
        conflict)
          ovpn_state_add_critical_issue CLIENT_IDENTITY_RECOVERY_CONFLICT RESTORE_BACKUP
          ;;
        invalid)
          ovpn_state_add_critical_issue CLIENT_IDENTITY_RECOVERY_INVALID RESTORE_BACKUP
          ;;
        *)
          ovpn_state_add_critical_issue CLIENT_IDENTITY_REGISTRY_INVALID RESTORE_CLIENT_IP_REGISTRY
          ;;
      esac
    fi
    return 0
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    [ "$status" = V ] || continue
    subject="${line##*$'\t'}"
    id="${subject##*/CN=}"
    id="${id%%/*}"
    [ -n "$id" ] || continue
    [ "$id" != "$OVPN_SERVER_NAME" ] || continue
    if ! ovpn_registry_uuid_valid "$id" || [ -z "${OVPN_REGISTRY_NAME_BY_ID[$id]:-}" ]; then
      ovpn_state_add_critical_issue "CLIENT_PKI_IDENTITY_UNKNOWN_$id" RESTORE_CLIENT_IP_REGISTRY
      continue
    fi
    name="${OVPN_REGISTRY_NAME_BY_ID[$id]}"
    serial="$(printf '%s\n' "$line" | awk -F '\t' 'NF >= 4 {print $4}')"
    profile="$OVPN_DATA_DIR/clients/active/$name.ovpn"
    if [ ! -e "$profile" ]; then
      ovpn_state_add_issue "CLIENT_PROFILE_MISSING_$name" repairable RENDER_CLIENT_PROFILE
      ovpn_state_consider DEGRADED_REPAIRABLE
    fi
    if [ ! -e "$OVPN_DATA_DIR/pki/issued/$id.crt" ] || [ ! -e "$OVPN_DATA_DIR/pki/private/$id.key" ]; then
      ovpn_state_classify_missing_client_identity "$name" "$id" "$serial"
    fi
  done <"$index"
}

ovpn_state_add_repairable_issue() {
  ovpn_state_add_issue "$1" repairable "$2"
  ovpn_state_consider DEGRADED_REPAIRABLE
}

ovpn_state_add_recoverable_issue() {
  ovpn_state_add_issue "$1" recoverable "$2"
  ovpn_state_consider DEGRADED_RECOVERABLE
}

ovpn_state_classify_missing_ca_cert() {
  if ovpn_recovery_assess_ca_cert; then
    ovpn_state_add_recoverable_issue CA_CERT_MISSING RECOVER_CA_CERT
    return 0
  fi

  case "$OVPN_RECOVERY_STATUS" in
    conflict)
      ovpn_state_add_critical_issue CRITICAL_RECOVERY_CONFLICT RESTORE_BACKUP
      ;;
    invalid)
      ovpn_state_add_critical_issue CA_CERT_RECOVERY_INVALID RESTORE_BACKUP
      ;;
    *)
      ovpn_state_add_critical_issue CA_CERT_MISSING RESTORE_BACKUP
      ;;
  esac
}

ovpn_state_classify_missing_tls_crypt_key() {
  if ovpn_recovery_assess_tls_crypt_key; then
    ovpn_state_add_recoverable_issue TLS_CRYPT_KEY_MISSING RECOVER_TLS_CRYPT_KEY
    return 0
  fi

  case "$OVPN_RECOVERY_STATUS" in
    conflict)
      ovpn_state_add_critical_issue CRITICAL_RECOVERY_CONFLICT RESTORE_BACKUP
      ;;
    invalid)
      ovpn_state_add_critical_issue TLS_CRYPT_KEY_RECOVERY_INVALID RESTORE_BACKUP
      ;;
    *)
      ovpn_state_add_critical_issue TLS_CRYPT_KEY_MISSING RESTORE_BACKUP
      ;;
  esac
}

ovpn_state_classify_missing_client_identity() {
  local name="$1"
  local id="$2"
  local serial="$3"
  local certificate="$OVPN_DATA_DIR/pki/issued/$id.crt"
  local key="$OVPN_DATA_DIR/pki/private/$id.key"

  if ovpn_recovery_assess_client_identity "$id" "$serial"; then
    [ -e "$certificate" ] || ovpn_state_add_recoverable_issue "CLIENT_CERT_MISSING_$name" RECOVER_CLIENT_CERT
    [ -e "$key" ] || ovpn_state_add_recoverable_issue "CLIENT_KEY_MISSING_$name" RECOVER_CLIENT_KEY
    return 0
  fi

  case "$OVPN_RECOVERY_STATUS" in
    conflict)
      ovpn_state_add_critical_issue CRITICAL_RECOVERY_CONFLICT RESTORE_BACKUP
      ;;
    invalid)
      ovpn_state_add_critical_issue "CLIENT_IDENTITY_RECOVERY_INVALID_$name" RESTORE_BACKUP
      ;;
    *)
      ovpn_state_add_critical_issue "CLIENT_IDENTITY_RECOVERY_UNAVAILABLE_$name" RESTORE_BACKUP
      ;;
  esac
}
ovpn_state_add_critical_issue() {
  ovpn_state_add_issue "$1" critical "$2"
  ovpn_state_consider CRITICAL
}

ovpn_state_metadata_ca_fingerprint() {
  local line

  while IFS= read -r line || [ -n "$line" ]; do
    if [[ "$line" =~ \"ca_fingerprint_sha256\"[[:space:]]*:[[:space:]]*\"([^\"]+)\" ]]; then
      printf '%s\n' "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"$OVPN_DATA_DIR/meta/instance.json"
  return 1
}

ovpn_state_crl_is_expired() {
  local openssl_bin="$1"
  local crl="$2"
  local next_update expires_at now

  next_update="$("$openssl_bin" crl -in "$crl" -noout -nextupdate 2>/dev/null || true)"
  next_update="${next_update#nextUpdate=}"
  [ -n "$next_update" ] || return 0
  expires_at="$(date -u -d "$next_update" +%s 2>/dev/null || true)"
  [ -n "$expires_at" ] || return 0
  now="$(date -u +%s)"
  [ "$expires_at" -le "$now" ]
}

ovpn_state_validate_crypto() {
  local openssl_bin
  local ca_cert="$OVPN_DATA_DIR/pki/ca.crt"
  local ca_key="$OVPN_DATA_DIR/pki/private/ca.key"
  local server_cert="$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt"
  local server_key="$OVPN_DATA_DIR/pki/private/$OVPN_SERVER_NAME.key"
  local crl="$OVPN_DATA_DIR/pki/crl.pem"
  local metadata="$OVPN_DATA_DIR/meta/instance.json"
  local ca_cert_valid=false ca_key_valid=false server_cert_valid=false server_key_valid=false
  local ca_cert_pub ca_key_pub server_cert_pub server_key_pub issuer subject metadata_fingerprint current_fingerprint fingerprint_output

  openssl_bin="$(ovpn_openssl_bin)" || {
    ovpn_state_add_critical_issue OPENSSL_UNAVAILABLE INSTALL_OPENSSL
    return 0
  }

  if [ -e "$ca_cert" ]; then
    if "$openssl_bin" x509 -in "$ca_cert" -noout >/dev/null 2>&1; then
      ca_cert_valid=true
    else
      ovpn_state_add_critical_issue CA_CERT_INVALID RESTORE_BACKUP
    fi
  fi
  if [ -e "$ca_key" ]; then
    if "$openssl_bin" pkey -in "$ca_key" -noout >/dev/null 2>&1; then
      ca_key_valid=true
    else
      ovpn_state_add_critical_issue CA_KEY_INVALID RESTORE_BACKUP
    fi
  fi
  if [ "$ca_cert_valid" = true ] && [ "$ca_key_valid" = true ]; then
    if ca_cert_pub="$("$openssl_bin" x509 -in "$ca_cert" -noout -pubkey 2>/dev/null)" && ca_key_pub="$("$openssl_bin" pkey -in "$ca_key" -pubout 2>/dev/null)"; then
      [ "$ca_cert_pub" = "$ca_key_pub" ] || ovpn_state_add_critical_issue CA_CERT_KEY_MISMATCH RESTORE_BACKUP
    else
      ovpn_state_add_critical_issue CA_PUBLIC_KEY_UNREADABLE RESTORE_BACKUP
    fi
  fi

  if [ -e "$server_cert" ]; then
    if "$openssl_bin" x509 -in "$server_cert" -noout >/dev/null 2>&1; then
      server_cert_valid=true
    else
      ovpn_state_add_critical_issue SERVER_CERT_INVALID RESTORE_BACKUP
    fi
  fi
  if [ -e "$server_key" ]; then
    if "$openssl_bin" pkey -in "$server_key" -noout >/dev/null 2>&1; then
      server_key_valid=true
    else
      ovpn_state_add_critical_issue SERVER_KEY_INVALID RESTORE_BACKUP
    fi
  fi
  if [ "$server_cert_valid" = true ] && [ "$server_key_valid" = true ]; then
    if server_cert_pub="$("$openssl_bin" x509 -in "$server_cert" -noout -pubkey 2>/dev/null)" && server_key_pub="$("$openssl_bin" pkey -in "$server_key" -pubout 2>/dev/null)"; then
      [ "$server_cert_pub" = "$server_key_pub" ] || ovpn_state_add_critical_issue SERVER_CERT_KEY_MISMATCH RESTORE_BACKUP
    else
      ovpn_state_add_critical_issue SERVER_PUBLIC_KEY_UNREADABLE RESTORE_BACKUP
    fi
  fi
  if [ "$ca_cert_valid" = true ] && [ "$server_cert_valid" = true ]; then
    "$openssl_bin" verify -CAfile "$ca_cert" "$server_cert" >/dev/null 2>&1 || ovpn_state_add_critical_issue SERVER_CERT_CA_MISMATCH RESTORE_BACKUP
    if ! "$openssl_bin" x509 -in "$server_cert" -noout -purpose 2>/dev/null | grep -Fq 'SSL server : Yes'; then
      ovpn_state_add_critical_issue SERVER_CERT_PURPOSE_INVALID RESTORE_BACKUP
    fi
  fi

  if [ -e "$crl" ] && [ "$ca_cert_valid" = true ]; then
    if "$openssl_bin" crl -in "$crl" -noout >/dev/null 2>&1; then
      issuer="$("$openssl_bin" crl -in "$crl" -noout -issuer 2>/dev/null || true)"
      subject="$("$openssl_bin" x509 -in "$ca_cert" -noout -subject 2>/dev/null || true)"
      if [ -n "$issuer" ] && [ "${issuer#*=}" = "${subject#*=}" ]; then
        if ovpn_state_crl_is_expired "$openssl_bin" "$crl"; then
          ovpn_state_add_repairable_issue CRL_EXPIRED REGENERATE_CRL
        fi
      else
        ovpn_state_add_repairable_issue CRL_CA_MISMATCH REGENERATE_CRL
      fi
    else
      ovpn_state_add_repairable_issue CRL_INVALID REGENERATE_CRL
    fi
  fi

  if [ -e "$metadata" ] && [ "$ca_cert_valid" = true ]; then
    metadata_fingerprint="$(ovpn_state_metadata_ca_fingerprint || true)"
    if [ -z "$metadata_fingerprint" ]; then
      ovpn_state_add_critical_issue METADATA_FINGERPRINT_MISSING REBUILD_METADATA
    else
      fingerprint_output="$("$openssl_bin" x509 -in "$ca_cert" -noout -fingerprint -sha256 2>/dev/null || true)"
      current_fingerprint="${fingerprint_output#*=}"
      [ -n "$current_fingerprint" ] && [ "$metadata_fingerprint" = "$current_fingerprint" ] || ovpn_state_add_critical_issue METADATA_CA_FINGERPRINT_MISMATCH RESTORE_BACKUP
    fi
  fi
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
    ovpn_state_classify_missing_ca_cert
  fi
  if [ ! -e "$OVPN_DATA_DIR/secrets/tls-crypt.key" ]; then
    ovpn_state_classify_missing_tls_crypt_key
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

  ovpn_state_validate_crypto
  if [ -e "$OVPN_DATA_DIR/pki/index.txt" ]; then
    ovpn_state_scan_client_profiles
    if [ "$OVPN_STATE_CLIENT_REGISTRY_RECOVERY_PENDING" != true ] &&
      declare -F ovpn_state_scan_ipam_consistency >/dev/null 2>&1; then
      ovpn_state_scan_ipam_consistency
    fi
  fi
}

ovpn_state_detect() {
  ovpn_state_scan
  printf '%s\n' "$OVPN_STATE"
}


ovpn_state_command() {
  local subcommand="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_state_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die "usage: ovpn state <show|doctor>"
  shift
  case "$subcommand" in
    show)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn state show" "Print the detected instance state."
      else
        [ "$#" -eq 0 ] || ovpn_die "usage: ovpn state show"
        ovpn_state_detect
      fi
      ;;
    doctor)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn state doctor [--json]" "Print detected issues and recommended actions."
      else
        ovpn_doctor_command "$@"
      fi
      ;;
    *) ovpn_die "usage: ovpn state <show|doctor>" ;;
  esac
}

ovpn_require_healthy_state() {
  local index

  ovpn_state_scan
  if [ "$OVPN_STATE" != HEALTHY ]; then
    ovpn_log "instance state is $OVPN_STATE"
    for ((index = 0; index < ${#OVPN_STATE_ISSUE_IDS[@]}; index++)); do
      ovpn_log "state issue: ${OVPN_STATE_ISSUE_IDS[index]} (action: ${OVPN_STATE_ISSUE_ACTIONS[index]})"
    done
    ovpn_exit_for_state "$OVPN_STATE"
  fi
}

ovpn_state_print_json_string() {
  local value="$1"

  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '"%s"' "$value"
}

ovpn_state_print_json() {
  local index

  printf '{\n'
  printf '  "state": '
  ovpn_state_print_json_string "$OVPN_STATE"
  printf ',\n'
  printf '  "issues": [\n'
  for ((index = 0; index < ${#OVPN_STATE_ISSUE_IDS[@]}; index++)); do
    printf '    {"id": '
    ovpn_state_print_json_string "${OVPN_STATE_ISSUE_IDS[index]}"
    printf ', "severity": '
    ovpn_state_print_json_string "${OVPN_STATE_ISSUE_SEVERITIES[index]}"
    printf ', "action": '
    ovpn_state_print_json_string "${OVPN_STATE_ISSUE_ACTIONS[index]}"
    printf '}'
    if [ "$index" -lt $((${#OVPN_STATE_ISSUE_IDS[@]} - 1)) ]; then
      printf ','
    fi
    printf '\n'
  done
  printf '  ]\n'
  printf '}\n'
}

ovpn_doctor_command() {
  local output_format=plain
  local index

  case "$#" in
    0)
      ;;
    1)
      if [ "$1" = --json ]; then
        output_format=json
      else
        ovpn_log "usage: ovpn state doctor [--json]"
        exit 64
      fi
      ;;
    *)
      ovpn_log "usage: ovpn state doctor [--json]"
      exit 64
      ;;
  esac

  ovpn_state_scan
  if [ "$output_format" = json ]; then
    ovpn_state_print_json
  else
    printf 'State: %s\n' "$OVPN_STATE"
    if [ "${#OVPN_STATE_ISSUE_IDS[@]}" -eq 0 ]; then
      printf 'Issues: none\n'
    else
      printf 'Issues:\n'
      for ((index = 0; index < ${#OVPN_STATE_ISSUE_IDS[@]}; index++)); do
        printf '  [%s] %s (action: %s)\n' \
          "${OVPN_STATE_ISSUE_SEVERITIES[index]}" \
          "${OVPN_STATE_ISSUE_IDS[index]}" \
          "${OVPN_STATE_ISSUE_ACTIONS[index]}"
      done
    fi
  fi

  case "$OVPN_STATE" in
    CRITICAL|UNRECOVERABLE)
      ovpn_exit_for_state "$OVPN_STATE"
      ;;
  esac
}
