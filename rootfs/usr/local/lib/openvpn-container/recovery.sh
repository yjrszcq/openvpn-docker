#!/usr/bin/env bash

OVPN_RECOVERY_STATUS=unavailable
OVPN_RECOVERY_SELECTED_VALUE=''
OVPN_RECOVERY_SELECTED_SOURCE=''
OVPN_RECOVERY_CANDIDATE_SOURCES=()
OVPN_RECOVERY_CANDIDATE_VALUES=()
OVPN_RECOVERY_CANDIDATE_HASHES=()
OVPN_RECOVERY_CLIENT_CERTIFICATE=''
OVPN_RECOVERY_CLIENT_KEY=''
declare -a OVPN_RECOVERY_CLIENT_IDS=()
declare -A OVPN_RECOVERY_CLIENT_NAMES=()
declare -A OVPN_RECOVERY_CLIENT_STATES=()
declare -A OVPN_RECOVERY_CLIENT_PROFILE_PATHS=()

ovpn_recovery_reset() {
  OVPN_RECOVERY_STATUS=unavailable
  OVPN_RECOVERY_SELECTED_VALUE=''
  OVPN_RECOVERY_SELECTED_SOURCE=''
  OVPN_RECOVERY_CANDIDATE_SOURCES=()
  OVPN_RECOVERY_CANDIDATE_VALUES=()
  OVPN_RECOVERY_CANDIDATE_HASHES=()
  OVPN_RECOVERY_CLIENT_CERTIFICATE=''
  OVPN_RECOVERY_CLIENT_KEY=''
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

ovpn_recovery_extract_profile_identity() {
  local profile="$1"
  local line id='' name='' id_count=0 name_count=0

  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      '# ovpn-client-id: '*)
        id="${line#\# ovpn-client-id: }"
        id_count=$((id_count + 1))
        ;;
      '# ovpn-client-name: '*)
        name="${line#\# ovpn-client-name: }"
        name_count=$((name_count + 1))
        ;;
    esac
  done <"$profile"
  [ "$id_count" -eq 1 ] && [ "$name_count" -eq 1 ] || return 1
  ovpn_registry_uuid_valid "$id" && ovpn_registry_client_name_valid "$name" || return 1
  printf '%s,%s\n' "$id" "$name"
}

ovpn_recovery_record_client_name() {
  local id="$1"
  local name="$2"
  local current

  [ -n "${OVPN_RECOVERY_CLIENT_STATES[$id]:-}" ] || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  current="${OVPN_RECOVERY_CLIENT_NAMES[$id]:-}"
  if [ -n "$current" ] && [ "$current" != "$name" ]; then
    OVPN_RECOVERY_STATUS=conflict
    return 1
  fi
  OVPN_RECOVERY_CLIENT_NAMES["$id"]="$name"
}

ovpn_recovery_load_ipam_layout() {
  local layout

  layout="$(
    ovpn_config_load
    printf '%s %s %s\n' \
      "$OVPN_IPAM_STATIC_CAPACITY" \
      "$OVPN_IPAM_STATIC_START_INT" \
      "$OVPN_IPAM_STATIC_END_INT"
  )" 2>/dev/null || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  read -r \
    OVPN_IPAM_STATIC_CAPACITY \
    OVPN_IPAM_STATIC_START_INT \
    OVPN_IPAM_STATIC_END_INT <<<"$layout"
}

ovpn_recovery_collect_client_pki() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject id
  local -A seen=()

  OVPN_RECOVERY_CLIENT_IDS=()
  OVPN_RECOVERY_CLIENT_NAMES=()
  OVPN_RECOVERY_CLIENT_STATES=()
  OVPN_RECOVERY_CLIENT_PROFILE_PATHS=()
  [ -r "$index" ] || {
    OVPN_RECOVERY_STATUS=unavailable
    return 1
  }
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in V|R) ;; *) continue ;; esac
    subject="${line##*$'\t'}"
    id="${subject##*/CN=}"
    id="${id%%/*}"
    [ "$id" = "$OVPN_SERVER_NAME" ] && continue
    ovpn_registry_uuid_valid "$id" || {
      [ "$status" = R ] && continue
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
    if [ -z "${seen[$id]+present}" ]; then
      OVPN_RECOVERY_CLIENT_IDS+=("$id")
      seen["$id"]=1
    fi
    if [ "$status" = V ]; then
      OVPN_RECOVERY_CLIENT_STATES["$id"]=active
    elif [ -z "${OVPN_RECOVERY_CLIENT_STATES[$id]:-}" ]; then
      OVPN_RECOVERY_CLIENT_STATES["$id"]=revoked
    fi
  done <"$index"
}

ovpn_recovery_collect_registry_names() {
  local file="$1"
  local line line_number=0 id name ip extra header_seen=false
  local -A ids=()
  local -A ips=()

  [ -e "$file" ] || return 0
  [ -r "$file" ] || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [ "$line" = '# id,name,ip' ]; then
      [ "$header_seen" = false ] && [ "$line_number" -eq 1 ] || {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
      header_seen=true
      continue
    fi
    [ "$header_seen" = true ] && [ -n "$line" ] && [[ "$line" != *[[:space:]]* ]] ||
      {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
    IFS=, read -r id name ip extra <<<"$line"
    [ -z "${extra:-}" ] && [[ "$line" == *,*,* ]] && [[ "$line" != *,*,*,* ]] ||
      {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
    ovpn_registry_uuid_valid "$id" && ovpn_registry_client_name_valid "$name" ||
      {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
    [ -z "${ids[$id]+present}" ] || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
    ids["$id"]=1
    if [ -n "$ip" ]; then
      ovpn_ipam_ipv4_to_int "$ip" >/dev/null 2>&1 || {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
      ovpn_ipam_ip_in_static_range "$ip" || {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
      [ -z "${ips[$ip]+present}" ] || {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
      ips["$ip"]=1
    fi
    ovpn_recovery_record_client_name "$id" "$name" || return 1
  done <"$file"
  [ "$header_seen" = true ] || {
    OVPN_RECOVERY_STATUS=invalid
    return 1
  }
}

ovpn_recovery_collect_profile_names() {
  local directory profile identity id name expected_state

  for directory in active revoked; do
    while IFS= read -r profile; do
      [ -n "$profile" ] || continue
      identity="$(ovpn_recovery_extract_profile_identity "$profile")" || {
        OVPN_RECOVERY_STATUS=invalid
        return 1
      }
      IFS=, read -r id name <<<"$identity"
      expected_state="${OVPN_RECOVERY_CLIENT_STATES[$id]:-}"
      [ "$expected_state" = "$directory" ] ||
        {
          OVPN_RECOVERY_STATUS=conflict
          return 1
        }
      ovpn_recovery_record_client_name "$id" "$name" || return 1
      [ -z "${OVPN_RECOVERY_CLIENT_PROFILE_PATHS[$id]:-}" ] ||
        {
          OVPN_RECOVERY_STATUS=conflict
          return 1
        }
      OVPN_RECOVERY_CLIENT_PROFILE_PATHS["$id"]="$profile"
    done < <(find "$OVPN_DATA_DIR/clients/$directory" -maxdepth 1 -type f -name '*.ovpn' -print 2>/dev/null | LC_ALL=C sort)
  done
}

ovpn_recovery_collect_audit_names() {
  local audit_file line id name
  local regex='^\{"timestamp":"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z","event":"client_rename","outcome":"applied","client_id":"([0-9a-f-]{36})","client_name":"([A-Za-z0-9._-]+)","old_name":"[A-Za-z0-9._-]+","legacy":false\}$'
  local -A latest=()

  audit_file="$(ovpn_registry_audit_file)"
  [ -e "$audit_file" ] || return 0
  if declare -F ovpn_state_ipam_audit_is_valid >/dev/null 2>&1 &&
    ! ovpn_state_ipam_audit_is_valid "$audit_file"; then
    OVPN_RECOVERY_STATUS=invalid
    return 1
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    if [[ "$line" =~ $regex ]]; then
      id="${BASH_REMATCH[1]}"
      name="${BASH_REMATCH[2]}"
      latest["$id"]="$name"
    fi
  done <"$audit_file"
  for id in "${!latest[@]}"; do
    [ -n "${OVPN_RECOVERY_CLIENT_STATES[$id]:-}" ] || continue
    ovpn_recovery_record_client_name "$id" "${latest[$id]}" || return 1
  done
}

ovpn_recovery_assess_client_registry() {
  local id name
  local -A names=()

  OVPN_RECOVERY_STATUS=unavailable
  ovpn_recovery_collect_client_pki || return 1
  ovpn_recovery_load_ipam_layout || return 1
  ovpn_recovery_collect_registry_names "$(ovpn_registry_client_ip_file)" || return 1
  ovpn_recovery_collect_profile_names || return 1
  ovpn_recovery_collect_audit_names || return 1
  for id in "${OVPN_RECOVERY_CLIENT_IDS[@]}"; do
    name="${OVPN_RECOVERY_CLIENT_NAMES[$id]:-}"
    if [ -z "$name" ]; then
      name="client-${id//-/}"
      OVPN_RECOVERY_CLIENT_NAMES["$id"]="$name"
    fi
    [ -z "${names[$name]+present}" ] || {
      OVPN_RECOVERY_STATUS=conflict
      return 1
    }
    names["$name"]="$id"
  done
  OVPN_RECOVERY_STATUS=recoverable
}

ovpn_recovery_stage_client_registry() {
  local destination="$1"
  local id

  ovpn_recovery_assess_client_registry ||
    ovpn_die "client identity registry recovery evidence is $OVPN_RECOVERY_STATUS"
  mkdir -p "$(dirname "$destination")"
  {
    printf '%s\n' '# id,name,state'
    for id in "${OVPN_RECOVERY_CLIENT_IDS[@]}"; do
      printf '%s,%s,%s\n' "$id" "${OVPN_RECOVERY_CLIENT_NAMES[$id]}" "${OVPN_RECOVERY_CLIENT_STATES[$id]}"
    done | LC_ALL=C sort -t, -k1,1
  } >"$destination"
  chmod 600 "$destination"
}

ovpn_recovery_stage_client_ip_registry() {
  local source="$1"
  local destination="$2"
  local line id ignored_name ip
  local -A assignments=()

  ovpn_recovery_assess_client_registry ||
    ovpn_die "client IP registry recovery evidence is $OVPN_RECOVERY_STATUS"
  if [ -r "$source" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      [ "$line" = '# id,name,ip' ] && continue
      IFS=, read -r id ignored_name ip <<<"$line"
      assignments["$id"]="$ip"
    done <"$source"
  fi

  OVPN_CLIENT_IP_IDS=()
  OVPN_CLIENT_IP_NAMES=()
  OVPN_CLIENT_IP_VALUES=()
  OVPN_CLIENT_IP_INTS=()
  for id in "${OVPN_RECOVERY_CLIENT_IDS[@]}"; do
    ip="${assignments[$id]:-}"
    OVPN_CLIENT_IP_IDS+=("$id")
    OVPN_CLIENT_IP_NAMES+=("${OVPN_RECOVERY_CLIENT_NAMES[$id]}")
    OVPN_CLIENT_IP_VALUES+=("$ip")
    if [ -n "$ip" ]; then
      OVPN_CLIENT_IP_INTS+=("$(ovpn_ipam_ipv4_to_int "$ip")")
    else
      OVPN_CLIENT_IP_INTS+=('')
    fi
  done
  mkdir -p "$(dirname "$destination")"
  ovpn_client_ip_write_canonical_file "$destination"
}

ovpn_recovery_rewrite_profile_identity() {
  local source="$1"
  local destination="$2"
  local id="$3"
  local name="$4"

  awk -v client_id="$id" -v client_name="$name" '
    /^# ovpn-client-id: / {
      print "# ovpn-client-id: " client_id
      ids++
      next
    }
    /^# ovpn-client-name: / {
      print "# ovpn-client-name: " client_name
      names++
      next
    }
    { print }
    END { if (ids != 1 || names != 1) exit 1 }
  ' "$source" >"$destination"
  chmod 600 "$destination"
}

ovpn_recovery_render_client_with_registry() (
  local name="$1"
  local destination="$2"
  local shadow="$3"

  rm -rf "$shadow"
  mkdir -p "$shadow/meta"
  cp -a "$OVPN_DATA_DIR/config" "$shadow/config"
  cp -a "$OVPN_DATA_DIR/pki" "$shadow/pki"
  cp -a "$OVPN_DATA_DIR/secrets" "$shadow/secrets"
  ovpn_recovery_stage_client_registry "$shadow/meta/client-state.csv"
  OVPN_DATA_DIR="$shadow"
  OVPN_CONFIG_DIR="$shadow/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  mkdir -p "$(dirname "$destination")"
  ovpn_write_or_print "$destination" "$(ovpn_render_client_content "$name")"
  rm -rf "$shadow"
)

ovpn_recovery_stage_client_profiles() {
  local destination="$1"
  local id name state source target old_basename shadow

  ovpn_recovery_assess_client_registry ||
    ovpn_die "client profile recovery evidence is $OVPN_RECOVERY_STATUS"
  rm -rf "$destination"
  if [ -d "$OVPN_DATA_DIR/clients" ]; then
    cp -a "$OVPN_DATA_DIR/clients" "$destination"
  else
    mkdir -p "$destination"
  fi
  mkdir -p "$destination/active" "$destination/revoked"
  shadow="$(dirname "$destination")/.identity-render"
  for id in "${OVPN_RECOVERY_CLIENT_IDS[@]}"; do
    name="${OVPN_RECOVERY_CLIENT_NAMES[$id]}"
    state="${OVPN_RECOVERY_CLIENT_STATES[$id]}"
    source="${OVPN_RECOVERY_CLIENT_PROFILE_PATHS[$id]:-}"
    target="$destination/$state/$name.ovpn"
    if [ -n "$source" ]; then
      old_basename="${source##*/}"
      ovpn_recovery_rewrite_profile_identity "$source" "$target.tmp" "$id" "$name"
      mv "$target.tmp" "$target"
      [ "$old_basename" = "$name.ovpn" ] || rm -f "$destination/$state/$old_basename"
    elif [ "$state" = active ]; then
      ovpn_recovery_render_client_with_registry "$name" "$target" "$shadow"
    fi
  done
  rm -rf "$shadow"
  chmod 700 "$destination" "$destination/active" "$destination/revoked"
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
  local id="$1"
  local line status serial subject found=''

  [ -r "$OVPN_DATA_DIR/pki/index.txt" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    [ "$status" = V ] || continue
    subject="${line##*$'\t'}"
    [ "$subject" = "/CN=$id" ] || continue
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
  local id="$2"
  local serial="$3"
  local openssl_bin subject certificate_serial expected_serial

  openssl_bin="$(ovpn_openssl_bin)" || return 1
  "$openssl_bin" x509 -noout >/dev/null 2>&1 <<<"$value" || return 1
  subject="$(printf '%s\n' "$value" | "$openssl_bin" x509 -noout -subject -nameopt RFC2253 2>/dev/null)" || return 1
  subject="${subject#subject=}"
  case ",$subject," in
    *",CN=$id,"*) ;;
    *) return 1 ;;
  esac
  certificate_serial="$(printf '%s\n' "$value" | "$openssl_bin" x509 -noout -serial 2>/dev/null)" || return 1
  certificate_serial="$(ovpn_recovery_normalize_serial "$certificate_serial")" || return 1
  expected_serial="$(ovpn_recovery_normalize_serial "$serial")" || return 1
  [ "$certificate_serial" = "$expected_serial" ] || return 1
  ovpn_recovery_verify_certificate_with_available_ca "$value"
}

ovpn_recovery_assess_client_identity() {
  local id="$1"
  local serial="$2"
  local name profile
  local certificate key certificate_status key_status certificate_pub key_pub local_value local_pub

  ovpn_recovery_reset
  name="$(ovpn_registry_name_by_id "$id")" || return 1
  profile="$OVPN_DATA_DIR/clients/active/$name.ovpn"
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
  ovpn_recovery_validate_client_cert "$certificate" "$id" "$serial" || {
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
  if [ -e "$OVPN_DATA_DIR/pki/issued/$id.crt" ]; then
    local_value="$(cat "$OVPN_DATA_DIR/pki/issued/$id.crt")"
    ovpn_recovery_validate_client_cert "$local_value" "$id" "$serial" || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
    [ "$(ovpn_recovery_candidate_hash "$local_value")" = "$(ovpn_recovery_candidate_hash "$certificate")" ] || {
      OVPN_RECOVERY_STATUS=invalid
      return 1
    }
  fi
  if [ -e "$OVPN_DATA_DIR/pki/private/$id.key" ]; then
    local_value="$(cat "$OVPN_DATA_DIR/pki/private/$id.key")"
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
  local id="$1"
  local destination="$2"
  local serial

  serial="$(ovpn_recovery_client_index_serial "$id")" || ovpn_die "unable to find active client serial: $id"
  ovpn_recovery_assess_client_identity "$id" "$serial" || ovpn_die "client certificate recovery evidence is $OVPN_RECOVERY_STATUS"
  ovpn_recovery_write_value "$destination" "$OVPN_RECOVERY_CLIENT_CERTIFICATE" 644
}

ovpn_recovery_stage_client_key() {
  local id="$1"
  local destination="$2"
  local serial

  serial="$(ovpn_recovery_client_index_serial "$id")" || ovpn_die "unable to find active client serial: $id"
  ovpn_recovery_assess_client_identity "$id" "$serial" || ovpn_die "client key recovery evidence is $OVPN_RECOVERY_STATUS"
  ovpn_recovery_write_value "$destination" "$OVPN_RECOVERY_CLIENT_KEY" 600
}
