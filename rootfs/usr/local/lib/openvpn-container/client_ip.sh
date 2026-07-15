#!/usr/bin/env bash

declare -a OVPN_CLIENT_IP_NAMES=()
declare -a OVPN_CLIENT_IP_VALUES=()
declare -a OVPN_CLIENT_IP_INTS=()
declare -A OVPN_CLIENT_IP_PKI_STATES=()

ovpn_client_ip_error() {
  ovpn_log "client-ip: $*"
  return 1
}

ovpn_client_ip_parse_file() {
  local file="$1"
  local line line_number=0 name ip ip_int records=0 header_seen=false
  local -A names=()
  local -A ips=()

  OVPN_CLIENT_IP_NAMES=()
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
    if [ "$line" = '# client,ip' ]; then
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
    if [[ "$line" =~ [[:space:]] ]] || [[ "$line" != *,* ]] || [[ "$line" == *,*,* ]]; then
      ovpn_client_ip_error "line $line_number must contain exactly client,ip without whitespace"
      return 1
    fi
    name="${line%%,*}"
    ip="${line#*,}"
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
    OVPN_CLIENT_IP_NAMES+=("$name")
    OVPN_CLIENT_IP_VALUES+=("$ip")
    OVPN_CLIENT_IP_INTS+=("$ip_int")
    records=$((records + 1))
  done <"$file"
}

ovpn_client_ip_collect_pki_clients() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name

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
    ovpn_registry_client_is_deleted "$name" && continue
    ovpn_registry_client_name_valid "$name" || {
      ovpn_client_ip_error "PKI index contains an invalid client name '$name'"
      return 1
    }
    if [ "$status" = V ]; then
      OVPN_CLIENT_IP_PKI_STATES["$name"]=active
    elif [ -z "${OVPN_CLIENT_IP_PKI_STATES[$name]+present}" ]; then
      OVPN_CLIENT_IP_PKI_STATES["$name"]=revoked
    fi
  done <"$index"
}

ovpn_client_ip_validate_logical_clients() {
  local index name
  local -A registry_names=()

  ovpn_client_ip_collect_pki_clients || return 1
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    registry_names["$name"]=1
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

  if ! (ovpn_config_load); then
    ovpn_client_ip_error 'persistent network configuration is invalid'
    return 1
  fi
  ovpn_config_load
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
  printf '%s\n' '# client,ip' >"$output"
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    [ -n "${OVPN_CLIENT_IP_VALUES[index]}" ] || continue
    printf '%010d,%s,%s\n' \
      "${OVPN_CLIENT_IP_INTS[index]}" \
      "${OVPN_CLIENT_IP_NAMES[index]}" \
      "${OVPN_CLIENT_IP_VALUES[index]}"
  done | LC_ALL=C sort -t, -k1,1 -k2,2 | while IFS=, read -r _ name ip; do
    printf '%s,%s\n' "$name" "$ip"
  done >>"$output"
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    [ -z "${OVPN_CLIENT_IP_VALUES[index]}" ] || continue
    printf '%s\n' "${OVPN_CLIENT_IP_NAMES[index]}"
  done | LC_ALL=C sort | while IFS= read -r name; do
    printf '%s,\n' "$name"
  done >>"$output"
  chmod 600 "$output"
}

ovpn_client_ip_atomic_install() {
  local source="$1"
  local destination="$2"

  cp "$source" "$destination.tmp"
  mv "$destination.tmp" "$destination"
  chmod 600 "$destination"
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
  backup="$(mktemp "$OVPN_DATA_DIR/meta/.client-ip.apply.XXXXXX")"
  candidate="${draft}.candidate.$$"
  cp "$snapshot" "$backup"
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

ovpn_client_ip_validate_command() {
  local draft

  draft="$(ovpn_registry_client_ip_file)"
  ovpn_client_ip_validate_file "$draft"
  printf 'client-ip registry draft is valid\n'
}

ovpn_client_ip_list_command() {
  local draft

  draft="$(ovpn_registry_client_ip_file)"
  [ -r "$draft" ] || ovpn_die "cannot read registry draft: $draft"
  cat "$draft"
}

ovpn_client_ip_edit_command() {
  local draft editor

  draft="$(ovpn_registry_client_ip_file)"
  [ -e "$draft" ] || ovpn_die "registry draft does not exist: $draft"
  editor="${OVPN_EDITOR:-${EDITOR:-nano}}"
  case "$editor" in
    *[[:space:]]*) ovpn_die 'OVPN_EDITOR must be a single executable path' ;;
  esac
  command -v "$editor" >/dev/null 2>&1 || ovpn_die "editor is not available: $editor"
  "$editor" "$draft"
}

ovpn_client_ip_command() {
  local subcommand="${1:-}"

  [ -n "$subcommand" ] || ovpn_die 'usage: ovpn client ip <list|validate|apply|edit>'
  shift
  case "$subcommand" in
    list)
      [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn client ip list'
      ovpn_client_ip_list_command
      ;;
    validate)
      [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn client ip validate'
      ovpn_client_ip_validate_command
      ;;
    apply)
      [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn client ip apply'
      ovpn_with_data_lock client ovpn_client_ip_apply_inner
      ;;
    edit)
      [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn client ip edit'
      ovpn_client_ip_edit_command
      ;;
    *)
      ovpn_die 'usage: ovpn client ip <list|validate|apply|edit>'
      ;;
  esac
}
