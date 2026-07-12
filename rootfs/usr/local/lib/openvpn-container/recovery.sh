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
      if (!inside || $0 !~ /^[0-9A-Fa-f]+$/) {
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
