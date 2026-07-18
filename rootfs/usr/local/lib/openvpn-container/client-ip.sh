#!/usr/bin/env bash

declare -a OVPN_CLIENT_IP_NAMES=()
declare -a OVPN_CLIENT_IP_IDS=()
declare -a OVPN_CLIENT_IP_VALUES=()
declare -a OVPN_CLIENT_IP_INTS=()
declare -A OVPN_CLIENT_IP_PKI_STATES=()

ovpn_client_ip_error() {
  ovpn_log "client-ip: $*"
  return 1
}

ovpn_client_ip_parse_file() {
  local file="$1"
  local line line_number=0 id name ip extra ip_int records=0 header_seen=false
  local -A ids=()
  local -A names=()
  local -A ips=()

  OVPN_CLIENT_IP_NAMES=()
  OVPN_CLIENT_IP_IDS=()
  OVPN_CLIENT_IP_VALUES=()
  OVPN_CLIENT_IP_INTS=()

  [ -r "$file" ] || {
    ovpn_client_ip_error "cannot read registry draft: $file"
    return 1
  }

  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    [ -n "$line" ] || {
      ovpn_client_ip_error "line $line_number is empty"
      return 1
    }
    if [ "$line" = '# id,name,ip' ]; then
      if [ "$records" -ne 0 ] || [ "$header_seen" = true ]; then
        ovpn_client_ip_error "line $line_number has an invalid header position"
        return 1
      fi
      header_seen=true
      continue
    fi
    case "$line" in
      \#*)
        ovpn_client_ip_error "line $line_number has an unsupported comment"
        return 1
        ;;
    esac
    if [[ "$line" =~ [[:space:]] ]] || [[ "$line" != *,*,* ]] || [[ "$line" == *,*,*,* ]]; then
      ovpn_client_ip_error "line $line_number must contain exactly id,name,ip without whitespace"
      return 1
    fi
    IFS=, read -r id name ip extra <<<"$line"
    [ -z "${extra:-}" ] || return 1
    ovpn_registry_uuid_valid "$id" || {
      ovpn_client_ip_error "line $line_number has an invalid client UUID"
      return 1
    }
    if [ -n "${ids[$id]+present}" ]; then
      ovpn_client_ip_error "line $line_number duplicates client UUID '$id'"
      return 1
    fi
    ids["$id"]=1
    ovpn_registry_client_name_valid "$name" || {
      ovpn_client_ip_error "line $line_number has an invalid client name"
      return 1
    }
    if [ -n "${names[$name]+present}" ]; then
      ovpn_client_ip_error "line $line_number duplicates client '$name'"
      return 1
    fi
    names["$name"]=1
    ip_int=''
    if [ -n "$ip" ]; then
      if ! ip_int="$(ovpn_ipam_ipv4_to_int "$ip" 2>/dev/null)"; then
        ovpn_client_ip_error "line $line_number has an invalid IPv4 address"
        return 1
      fi
      if [ -n "${ips[$ip]+present}" ]; then
        ovpn_client_ip_error "line $line_number duplicates static IP '$ip'"
        return 1
      fi
      ips["$ip"]=1
      if ! ovpn_ipam_ip_in_static_range "$ip"; then
        ovpn_client_ip_error "line $line_number static IP '$ip' is outside the static address region"
        return 1
      fi
    fi
    OVPN_CLIENT_IP_IDS+=("$id")
    OVPN_CLIENT_IP_NAMES+=("$name")
    OVPN_CLIENT_IP_VALUES+=("$ip")
    OVPN_CLIENT_IP_INTS+=("$ip_int")
    records=$((records + 1))
  done <"$file"
  [ "$header_seen" = true ] || {
    ovpn_client_ip_error 'registry is missing the id,name,ip header'
    return 1
  }
}

ovpn_client_ip_collect_pki_clients() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name id identity_state
  local -A pki_states=()

  OVPN_CLIENT_IP_PKI_STATES=()
  [ -r "$index" ] || {
    ovpn_client_ip_error "cannot read PKI index: $index"
    return 1
  }
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in
      V|R) ;;
      *) continue ;;
    esac
    subject="${line##*$'\t'}"
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ -n "$name" ] || {
      ovpn_client_ip_error 'PKI index contains a logical client without a CN'
      return 1
    }
    [ "$name" = "$OVPN_SERVER_NAME" ] && continue
    ovpn_registry_client_name_valid "$name" || {
      ovpn_client_ip_error "PKI index contains an invalid client name '$name'"
      return 1
    }
    if [ "$status" = V ]; then
      pki_states["$name"]=active
    elif [ -z "${pki_states[$name]+present}" ]; then
      pki_states["$name"]=revoked
    fi
  done <"$index"
  ovpn_registry_load_identities || {
    ovpn_client_ip_error 'cannot read the authoritative client identity registry'
    return 1
  }
  for id in "${OVPN_REGISTRY_CLIENT_IDS[@]}"; do
    name="${OVPN_REGISTRY_NAME_BY_ID[$id]}"
    identity_state="${OVPN_REGISTRY_STATE_BY_ID[$id]}"
    if [ "$identity_state" = deleted ]; then
      [ -n "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[$name]:-}" ] || unset 'pki_states[$name]'
      continue
    fi
    [ "${pki_states[$name]:-}" = "$identity_state" ] || {
      ovpn_client_ip_error "identity registry and PKI disagree for client '$name'"
      return 1
    }
    OVPN_CLIENT_IP_PKI_STATES["$name"]="$identity_state"
    unset 'pki_states[$name]'
  done
  [ "${#pki_states[@]}" -eq 0 ] || {
    ovpn_client_ip_error 'PKI contains a client missing from the identity registry'
    return 1
  }
}

ovpn_client_ip_validate_logical_clients() {
  local index id name
  local -A registry_names=()

  ovpn_client_ip_collect_pki_clients || return 1
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    id="${OVPN_CLIENT_IP_IDS[index]}"
    registry_names["$name"]=1
    [ "${OVPN_REGISTRY_NAME_BY_ID[$id]:-}" = "$name" ] &&
      [ "${OVPN_REGISTRY_STATE_BY_ID[$id]:-}" != deleted ] || {
      ovpn_client_ip_error "registry identity does not match client '$name'"
      return 1
    }
    if [ -z "${OVPN_CLIENT_IP_PKI_STATES[$name]+present}" ]; then
      ovpn_client_ip_error "registry contains unknown client '$name'"
      return 1
    fi
  done
  for name in "${!OVPN_CLIENT_IP_PKI_STATES[@]}"; do
    if [ -z "${registry_names[$name]+present}" ]; then
      ovpn_client_ip_error "registry is missing logical client '$name'"
      return 1
    fi
  done
}

ovpn_client_ip_validate_file() {
  local file="$1"
  local static_count=0 index

  ovpn_config_load || {
    ovpn_client_ip_error 'persistent network configuration is invalid'
    return 1
  }
  ovpn_client_ip_parse_file "$file" || return 1
  ovpn_client_ip_validate_logical_clients || return 1
  for ((index = 0; index < ${#OVPN_CLIENT_IP_VALUES[@]}; index++)); do
    [ -z "${OVPN_CLIENT_IP_VALUES[index]}" ] || static_count=$((static_count + 1))
  done
  if [ "$static_count" -gt "$OVPN_IPAM_STATIC_CAPACITY" ]; then
    ovpn_client_ip_error "registry has $static_count static clients but capacity is $OVPN_IPAM_STATIC_CAPACITY"
    return 1
  fi
}

ovpn_client_ip_write_canonical_file() {
  local output="$1"
  local index

  umask 077
  printf '%s\n' '# id,name,ip' >"$output"
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    [ -n "${OVPN_CLIENT_IP_VALUES[index]}" ] || continue
    printf '%010d,%s,%s,%s\n' \
      "${OVPN_CLIENT_IP_INTS[index]}" \
      "${OVPN_CLIENT_IP_IDS[index]}" \
      "${OVPN_CLIENT_IP_NAMES[index]}" \
      "${OVPN_CLIENT_IP_VALUES[index]}"
  done | LC_ALL=C sort -t, -k1,1 -k3,3 -k2,2 | while IFS=, read -r _ id name ip; do
    printf '%s,%s,%s\n' "$id" "$name" "$ip"
  done >>"$output"
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    [ -z "${OVPN_CLIENT_IP_VALUES[index]}" ] || continue
    printf '%s,%s\n' "${OVPN_CLIENT_IP_NAMES[index]}" "${OVPN_CLIENT_IP_IDS[index]}"
  done | LC_ALL=C sort -t, -k1,1 -k2,2 | while IFS=, read -r name id; do
    printf '%s,%s,\n' "$id" "$name"
  done >>"$output"
  chmod 600 "$output"
}

ovpn_client_ip_atomic_install() {
  local source="$1"
  local destination="$2"

  cp "$source" "$destination.tmp" && mv "$destination.tmp" "$destination" && chmod 600 "$destination"
}

ovpn_client_ip_audit_event() {
  local outcome="$1"
  local audit_file

  audit_file="$(ovpn_registry_audit_file)"
  printf '{"timestamp":"%s","event":"client_ip_apply","outcome":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$outcome" >>"$audit_file"
  chmod 600 "$audit_file"
}

ovpn_client_ip_apply_begin() {
  return 0
}

ovpn_client_ip_apply_derived() {
  return 0
}

ovpn_client_ip_apply_finalize() {
  return 0
}

ovpn_client_ip_apply_rollback() {
  return 0
}

ovpn_client_ip_apply_inner() (
  local draft snapshot backup candidate transaction_success=false

  draft="$(ovpn_registry_client_ip_file)"
  snapshot="$(ovpn_registry_applied_file)"
  [ -r "$draft" ] || ovpn_die "cannot read registry draft: $draft"
  [ -r "$snapshot" ] || ovpn_die "cannot read applied registry snapshot: $snapshot"
  backup="$(mktemp "$OVPN_DATA_DIR/meta/.client-ip.apply.XXXXXX")" || ovpn_die "failed to create client-ip apply backup file"
  candidate="${draft}.candidate.$$"
  cp "$snapshot" "$backup" || ovpn_die "failed to backup applied client-IP registry"
  trap '
    status=$?
    trap - EXIT
    set +e
    if [ "$transaction_success" != true ]; then
      ovpn_client_ip_atomic_install "$backup" "$draft"
      ovpn_client_ip_atomic_install "$backup" "$snapshot"
      ovpn_client_ip_audit_event rejected || true
      ovpn_client_ip_apply_rollback || true
    fi
    rm -f "$backup" "$candidate"
    exit "$status"
  ' EXIT

  if ! ovpn_client_ip_validate_file "$draft"; then
    exit 1
  fi
  ovpn_client_ip_apply_begin
  ovpn_client_ip_write_canonical_file "$candidate"
  ovpn_client_ip_atomic_install "$candidate" "$draft"
  ovpn_client_ip_apply_derived
  ovpn_client_ip_atomic_install "$candidate" "$snapshot"
  ovpn_client_ip_audit_event applied
  ovpn_client_ip_apply_finalize
  transaction_success=true
  printf 'client-ip registry applied\n'
)
