#!/usr/bin/env bash

OVPN_RECOVERY_STATUS=unavailable
OVPN_RECOVERY_SELECTED_VALUE=''
OVPN_RECOVERY_SELECTED_SOURCE=''
OVPN_RECOVERY_CANDIDATE_SOURCES=()
OVPN_RECOVERY_CANDIDATE_VALUES=()
OVPN_RECOVERY_CANDIDATE_HASHES=()

ovpn_recovery_reset() {
  OVPN_RECOVERY_STATUS=unavailable
  OVPN_RECOVERY_SELECTED_VALUE=''
  OVPN_RECOVERY_SELECTED_SOURCE=''
  OVPN_RECOVERY_CANDIDATE_SOURCES=()
  OVPN_RECOVERY_CANDIDATE_VALUES=()
  OVPN_RECOVERY_CANDIDATE_HASHES=()
}

ovpn_recovery_profile_paths() (
  local profile

  shopt -s nullglob
  for profile in "$OVPN_DATA_DIR"/clients/active/*.ovpn "$OVPN_DATA_DIR"/clients/revoked/*.ovpn "$OVPN_DATA_DIR"/clients/archive/*.ovpn; do
    [ -f "$profile" ] && printf '%s\n' "$profile"
  done
)

ovpn_recovery_extract_profile_block() {
  local profile="$1"
  local tag="$2"

  awk -v open="<$tag>" -v closing_tag="</$tag>" '
    BEGIN { inside = 0; seen = 0; invalid = 0 }
    $0 == open {
      if (inside || seen) {
        invalid = 1
      } else {
        inside = 1
        seen = 1
      }
      next
    }
    $0 == closing_tag {
      if (!inside) {
        invalid = 1
      } else {
        inside = 0
      }
      next
    }
    { if (inside) print }
    END {
      if (inside || invalid) exit 2
      if (!seen) exit 3
    }
  ' "$profile"
}

ovpn_recovery_tls_crypt_is_valid() {
  local value="$1"

  printf '%s\n' "$value" | awk '
    $0 == "-----BEGIN OpenVPN Static key V1-----" {
      if (inside || seen_begin || seen_end) {
        invalid = 1
      } else {
        inside = 1
        seen_begin = 1
      }
      next
    }
    $0 == "-----END OpenVPN Static key V1-----" {
      if (!inside || seen_end) {
        invalid = 1
      } else {
        inside = 0
        seen_end = 1
      }
      next
    }
    {
      if (!inside) {
        if ($0 != "" && $0 !~ /^#/) invalid = 1
      } else if ($0 !~ /^[0-9A-Fa-f]+$/) {
        invalid = 1
      } else {
        hex_length += length($0)
      }
    }
    END {
      if (inside || invalid || !seen_begin || !seen_end || hex_length != 512) exit 1
    }
  '
}

ovpn_recovery_candidate_hash() {
  printf '%s\n' "$1" | sha256sum | awk '{print $1}'
}
ovpn_recovery_collect_profile_candidates() {
  local tag="$1"
  local validator="$2"
  local profile value status hash

  ovpn_recovery_reset
  while IFS= read -r profile; do
    if value="$(ovpn_recovery_extract_profile_block "$profile" "$tag")"; then
      status=0
    else
      status=$?
    fi
    case "$status" in
      0)
        "$validator" "$value" || {
          OVPN_RECOVERY_STATUS=invalid
          return 1
        }
        hash="$(ovpn_recovery_candidate_hash "$value")"
        OVPN_RECOVERY_CANDIDATE_SOURCES+=("$profile")
        OVPN_RECOVERY_CANDIDATE_VALUES+=("$value")
        OVPN_RECOVERY_CANDIDATE_HASHES+=("$hash")
        ;;
      3)
        ;;
      *)
        OVPN_RECOVERY_STATUS=invalid
        return 1
        ;;
    esac
  done < <(ovpn_recovery_profile_paths)

  if [ "${#OVPN_RECOVERY_CANDIDATE_VALUES[@]}" -eq 0 ]; then
    OVPN_RECOVERY_STATUS=unavailable
    return 1
  fi
  for hash in "${OVPN_RECOVERY_CANDIDATE_HASHES[@]}"; do
    if [ "$hash" != "${OVPN_RECOVERY_CANDIDATE_HASHES[0]}" ]; then
      OVPN_RECOVERY_STATUS=conflict
      return 1
    fi
  done

  OVPN_RECOVERY_SELECTED_VALUE="${OVPN_RECOVERY_CANDIDATE_VALUES[0]}"
  OVPN_RECOVERY_SELECTED_SOURCE="${OVPN_RECOVERY_CANDIDATE_SOURCES[0]}"
  OVPN_RECOVERY_STATUS=recoverable
}

ovpn_recovery_ca_candidate_is_valid() {
  local value="$1"
  local openssl_bin

  openssl_bin="$(ovpn_openssl_bin)" || return 1
  printf '%s\n' "$value" | "$openssl_bin" x509 -noout >/dev/null 2>&1
}

ovpn_recovery_tls_crypt_candidate_is_valid() {
  ovpn_recovery_tls_crypt_is_valid "$1"
}

ovpn_recovery_assess_ca_cert() {
  local openssl_bin ca_key server_cert candidate_pub ca_key_pub

  ovpn_recovery_collect_profile_candidates ca ovpn_recovery_ca_candidate_is_valid || return 1
  openssl_bin="$(ovpn_openssl_bin)" || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  ca_key="$OVPN_DATA_DIR/pki/private/ca.key"
  server_cert="$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt"
  if [ ! -r "$ca_key" ] || [ ! -r "$server_cert" ]; then
    OVPN_RECOVERY_STATUS=unavailable
    return 1
  fi
  candidate_pub="$(printf '%s\n' "$OVPN_RECOVERY_SELECTED_VALUE" | "$openssl_bin" x509 -noout -pubkey 2>/dev/null)" || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  ca_key_pub="$("$openssl_bin" pkey -in "$ca_key" -pubout 2>/dev/null)" || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  if [ "$candidate_pub" != "$ca_key_pub" ] || ! "$openssl_bin" verify -CAfile <(printf '%s\n' "$OVPN_RECOVERY_SELECTED_VALUE") "$server_cert" >/dev/null 2>&1; then
    OVPN_RECOVERY_STATUS=invalid
    return 1
  fi
  OVPN_RECOVERY_STATUS=recoverable
}

ovpn_recovery_assess_tls_crypt_key() {
  ovpn_recovery_collect_profile_candidates tls-crypt ovpn_recovery_tls_crypt_candidate_is_valid
}

ovpn_recovery_write_value() {
  local destination="$1"
  local value="$2"
  local mode="$3"

  mkdir -p "$(dirname "$destination")"
  umask 077
  printf '%s\n' "$value" >"$destination.tmp"
  mv "$destination.tmp" "$destination"
  chmod "$mode" "$destination"
}

ovpn_recovery_stage_ca_cert() {
  local destination="$1"

  ovpn_recovery_assess_ca_cert || ovpn_die "CA certificate recovery evidence is $OVPN_RECOVERY_STATUS"
  ovpn_recovery_write_value "$destination" "$OVPN_RECOVERY_SELECTED_VALUE" 644
}

ovpn_recovery_stage_tls_crypt_key() {
  local destination="$1"

  ovpn_recovery_assess_tls_crypt_key || ovpn_die "tls-crypt recovery evidence is $OVPN_RECOVERY_STATUS"
  ovpn_recovery_write_value "$destination" "$OVPN_RECOVERY_SELECTED_VALUE" 600
}

ovpn_recovery_normalize_serial() {
  local serial="${1#serial=}"

  serial="${serial^^}"
  [[ "$serial" =~ ^[0-9A-F]+$ ]] || return 1
  while [ "${#serial}" -gt 1 ] && [ "${serial:0:1}" = 0 ]; do
    serial="${serial:1}"
  done
  printf '%s\n' "$serial"
}

ovpn_recovery_client_index_serial() {
  local name="$1"
  local line status serial subject found=''

  [ -r "$OVPN_DATA_DIR/pki/index.txt" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    [ "$status" = V ] || continue
    subject="${line##*$'\t'}"
    [ "$subject" = "/CN=$name" ] || continue
    serial="$(printf '%s\n' "$line" | awk -F '\t' 'NF >= 4 {print $4}')"
    [ -n "$serial" ] || return 1
    [ -z "$found" ] || return 1
    found="$serial"
  done <"$OVPN_DATA_DIR/pki/index.txt"
  [ -n "$found" ] || return 1
  printf '%s\n' "$found"
}

ovpn_recovery_cert_public_key() {
  local value="$1"
  local openssl_bin

  openssl_bin="$(ovpn_openssl_bin)" || return 1
  printf '%s\n' "$value" | "$openssl_bin" x509 -noout -pubkey 2>/dev/null
}

ovpn_recovery_key_public_key() {
  local value="$1"
  local openssl_bin

  openssl_bin="$(ovpn_openssl_bin)" || return 1
  printf '%s\n' "$value" | "$openssl_bin" pkey -pubout 2>/dev/null
}

ovpn_recovery_verify_certificate_with_available_ca() {
  local certificate="$1"
  local openssl_bin

  openssl_bin="$(ovpn_openssl_bin)" || return 1
  if [ -r "$OVPN_DATA_DIR/pki/ca.crt" ]; then
    "$openssl_bin" verify -CAfile "$OVPN_DATA_DIR/pki/ca.crt" <(printf '%s\n' "$certificate") >/dev/null 2>&1
    return $?
  fi
  ovpn_recovery_assess_ca_cert || return 1
  "$openssl_bin" verify -CAfile <(printf '%s\n' "$OVPN_RECOVERY_SELECTED_VALUE") <(printf '%s\n' "$certificate") >/dev/null 2>&1
}

ovpn_recovery_validate_client_cert() {
  local value="$1"
  local name="$2"
  local serial="$3"
  local openssl_bin subject certificate_serial expected_serial

  openssl_bin="$(ovpn_openssl_bin)" || return 1
  "$openssl_bin" x509 -noout >/dev/null 2>&1 <<<"$value" || return 1
  subject="$(printf '%s\n' "$value" | "$openssl_bin" x509 -noout -subject -nameopt RFC2253 2>/dev/null)" || return 1
  subject="${subject#subject=}"
  case ",$subject," in
    *",CN=$name,"*) ;;
    *) return 1 ;;
  esac
  certificate_serial="$(printf '%s\n' "$value" | "$openssl_bin" x509 -noout -serial 2>/dev/null)" || return 1
  certificate_serial="$(ovpn_recovery_normalize_serial "$certificate_serial")" || return 1
  expected_serial="$(ovpn_recovery_normalize_serial "$serial")" || return 1
  [ "$certificate_serial" = "$expected_serial" ] || return 1
  ovpn_recovery_verify_certificate_with_available_ca "$value"
}

ovpn_recovery_assess_client_identity() {
  local name="$1"
  local serial="$2"
  local profile="$OVPN_DATA_DIR/clients/active/$name.ovpn"
  local certificate key certificate_status key_status certificate_pub key_pub local_value local_pub

  ovpn_recovery_reset
  [ -r "$profile" ] || return 1
  if certificate="$(ovpn_recovery_extract_profile_block "$profile" cert)"; then
    certificate_status=0
  else
    certificate_status=$?
  fi
  if key="$(ovpn_recovery_extract_profile_block "$profile" key)"; then
    key_status=0
  else
    key_status=$?
  fi
  if [ "$certificate_status" -ne 0 ] || [ "$key_status" -ne 0 ]; then
    if [ "$certificate_status" -eq 3 ] || [ "$key_status" -eq 3 ]; then
      OVPN_RECOVERY_STATUS=unavailable
    else
      OVPN_RECOVERY_STATUS=invalid
    fi
    return 1
  fi
  ovpn_recovery_validate_client_cert "$certificate" "$name" "$serial" || {
    [ "$OVPN_RECOVERY_STATUS" = conflict ] || OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  certificate_pub="$(ovpn_recovery_cert_public_key "$certificate")" || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  key_pub="$(ovpn_recovery_key_public_key "$key")" || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  [ "$certificate_pub" = "$key_pub" ] || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  if [ -e "$OVPN_DATA_DIR/pki/issued/$name.crt" ]; then
    local_value="$(cat "$OVPN_DATA_DIR/pki/issued/$name.crt")"
    ovpn_recovery_validate_client_cert "$local_value" "$name" "$serial" || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
    [ "$(ovpn_recovery_candidate_hash "$local_value")" = "$(ovpn_recovery_candidate_hash "$certificate")" ] || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
  fi
  if [ -e "$OVPN_DATA_DIR/pki/private/$name.key" ]; then
    local_value="$(cat "$OVPN_DATA_DIR/pki/private/$name.key")"
    local_pub="$(ovpn_recovery_key_public_key "$local_value")" || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
    [ "$local_pub" = "$certificate_pub" ] || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
  fi
  OVPN_RECOVERY_CLIENT_CERTIFICATE="$certificate"
  OVPN_RECOVERY_CLIENT_KEY="$key"
  OVPN_RECOVERY_STATUS=recoverable
}

ovpn_recovery_stage_client_certificate() {
  local name="$1"
  local destination="$2"
  local serial

  serial="$(ovpn_recovery_client_index_serial "$name")" || ovpn_die "unable to find active client serial: $name"
  ovpn_recovery_assess_client_identity "$name" "$serial" || ovpn_die "client certificate recovery evidence is $OVPN_RECOVERY_STATUS"
  ovpn_recovery_write_value "$destination" "$OVPN_RECOVERY_CLIENT_CERTIFICATE" 644
}

ovpn_recovery_stage_client_key() {
  local name="$1"
  local destination="$2"
  local serial

  serial="$(ovpn_recovery_client_index_serial "$name")" || ovpn_die "unable to find active client serial: $name"
  ovpn_recovery_assess_client_identity "$name" "$serial" || ovpn_die "client key recovery evidence is $OVPN_RECOVERY_STATUS"
  ovpn_recovery_write_value "$destination" "$OVPN_RECOVERY_CLIENT_KEY" 600
}
