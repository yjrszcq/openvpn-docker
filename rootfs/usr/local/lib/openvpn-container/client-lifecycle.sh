#!/usr/bin/env bash

ovpn_client_records() {
  local name id

  ovpn_client_ip_collect_pki_clients || return 1
  for name in "${!OVPN_CLIENT_IP_PKI_STATES[@]}"; do
    id="${OVPN_REGISTRY_CURRENT_ID_BY_NAME[$name]}"
    printf '%s %s %s\n' "$name" "$id" "${OVPN_CLIENT_IP_PKI_STATES[$name]}"
  done | LC_ALL=C sort
}

declare -A OVPN_CLIENT_LIST_CONNECTED_IPS=()
declare -A OVPN_CLIENT_LIST_PERSISTED_IPS=()
OVPN_CLIENT_LIST_MANAGEMENT_AVAILABLE=false

ovpn_client_list_prepare_registry() {
  local registry

  registry="$(ovpn_registry_client_ip_file)"
  [ -r "$registry" ] || ovpn_die "cannot read client-IP registry: $registry"
  ovpn_client_ip_validate_file "$registry" || ovpn_die 'client-IP registry is invalid; restore it before listing IPs'
}

ovpn_client_list_dynamic_ip_is_valid() {
  local id="$1"
  local address="$2"

  ovpn_registry_uuid_valid "$id" || return 1
  [ "${OVPN_REGISTRY_STATE_BY_ID[$id]:-}" = active ] || return 1
  ovpn_ipam_ip_in_dynamic_pool "$address"
}

ovpn_client_list_connected_client_is_valid() {
  local id="$1"
  local address="$2"

  ovpn_registry_uuid_valid "$id" || return 1
  [ "${OVPN_REGISTRY_STATE_BY_ID[$id]:-}" = active ] || return 1
  ovpn_ipam_ipv4_to_int "$address" >/dev/null
}

ovpn_client_list_load_persisted_dynamic_ips() {
  local lease_dir="$OVPN_LEASE_DIR"
  local id name address f

  OVPN_CLIENT_LIST_PERSISTED_IPS=()
  [ -d "$lease_dir" ] || return 0
  for f in "$lease_dir"/*; do
    [ -f "$f" ] || continue
    id="$(basename "$f")"
    address="$(cat "$f" 2>/dev/null || true)"
    ovpn_client_list_dynamic_ip_is_valid "$id" "$address" || continue
    name="${OVPN_REGISTRY_NAME_BY_ID[$id]}"
    [ -n "${OVPN_CLIENT_LIST_PERSISTED_IPS[$name]+present}" ] && continue
    OVPN_CLIENT_LIST_PERSISTED_IPS["$name"]="$address"
  done
}

ovpn_client_list_load_connected_clients() {
  local response line address id name ignored_field
  local in_routing_table=false

  OVPN_CLIENT_LIST_CONNECTED_IPS=()
  OVPN_CLIENT_LIST_MANAGEMENT_AVAILABLE=false
  response="$(ovpn_management_socket_request "$OVPN_MANAGEMENT_SOCKET" "status 3")" || return 0
  response="${response//$'\r'/}"
  case $'\n'"$response"$'\n' in
    *$'\nEND\n'*) OVPN_CLIENT_LIST_MANAGEMENT_AVAILABLE=true ;;
    *) return 0 ;;
  esac
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      ROUTING_TABLE$'\t'*)
        IFS=$'\t' read -r ignored_field address id ignored_field <<<"$line"
        ;;
      ROUTING_TABLE,*)
        IFS=, read -r ignored_field address id ignored_field <<<"$line"
        ;;
      'ROUTING TABLE')
        in_routing_table=true
        continue
        ;;
      'GLOBAL STATS'|END)
        in_routing_table=false
        continue
        ;;
      *)
        [ "$in_routing_table" = true ] || continue
        IFS=, read -r address id ignored_field <<<"$line"
        ;;
    esac
    ovpn_client_list_connected_client_is_valid "$id" "$address" || continue
    name="${OVPN_REGISTRY_NAME_BY_ID[$id]}"
    [ -n "${OVPN_CLIENT_LIST_CONNECTED_IPS[$name]+present}" ] && continue
    OVPN_CLIENT_LIST_CONNECTED_IPS["$name"]="$address"
  done <<<"$response"
}

ovpn_client_list_with_ip_command() {
  local index id name state assignment address ip_state connection mode row
  local id_width=9 name_width=4 state_width=5 mode_width=4 ip_width=2 ip_state_width=8
  local -a rows=()

  ovpn_require_healthy_state
  ovpn_client_list_prepare_registry
  ovpn_client_list_load_persisted_dynamic_ips
  ovpn_client_list_load_connected_clients
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    id="${OVPN_CLIENT_IP_IDS[index]}"
    name="${OVPN_CLIENT_IP_NAMES[index]}"
    state="${OVPN_CLIENT_IP_PKI_STATES[$name]}"
    assignment="${OVPN_CLIENT_IP_VALUES[index]}"
    connection=unknown
    if [ "$OVPN_CLIENT_LIST_MANAGEMENT_AVAILABLE" = true ]; then
      connection=offline
      [ -z "${OVPN_CLIENT_LIST_CONNECTED_IPS[$name]+present}" ] || connection=online
    fi
    if [ -n "$assignment" ]; then
      mode=static
      address="$assignment"
      ip_state=configured
      [ "$state" != revoked ] || ip_state=retained
    else
      mode=dynamic
      address='-'
      ip_state='unavailable'
      if [ -n "${OVPN_CLIENT_LIST_CONNECTED_IPS[$name]+present}" ] && ovpn_ipam_ip_in_dynamic_pool "${OVPN_CLIENT_LIST_CONNECTED_IPS[$name]}"; then
        address="${OVPN_CLIENT_LIST_CONNECTED_IPS[$name]}"
        ip_state='connected'
      elif [ -n "${OVPN_CLIENT_LIST_PERSISTED_IPS[$name]+present}" ]; then
        address="${OVPN_CLIENT_LIST_PERSISTED_IPS[$name]}"
        ip_state='last-known'
      fi
    fi
    rows+=("$name"$'\t'"$id"$'\t'"$state"$'\t'"$mode"$'\t'"$address"$'\t'"$ip_state"$'\t'"$connection")
  done

  if ((${#rows[@]})); then
    mapfile -t rows < <(printf '%s\n' "${rows[@]}" | LC_ALL=C sort)
  fi
  for row in "${rows[@]}"; do
    IFS=$'\t' read -r name id state mode address ip_state connection <<<"$row"
    if ((${#id} > id_width)); then id_width=${#id}; fi
    if ((${#name} > name_width)); then name_width=${#name}; fi
    if ((${#state} > state_width)); then state_width=${#state}; fi
    if ((${#mode} > mode_width)); then mode_width=${#mode}; fi
    if ((${#address} > ip_width)); then ip_width=${#address}; fi
    if ((${#ip_state} > ip_state_width)); then ip_state_width=${#ip_state}; fi
  done
  printf '%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s\n' \
    "$id_width" 'CLIENT ID' "$name_width" NAME "$state_width" STATE "$mode_width" MODE "$ip_width" IP \
    "$ip_state_width" 'IP STATE' CONNECTION
  for row in "${rows[@]}"; do
    IFS=$'\t' read -r name id state mode address ip_state connection <<<"$row"
    printf '%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s\n' \
      "$id_width" "$id" "$name_width" "$name" "$state_width" "$state" "$mode_width" "$mode" "$ip_width" "$address" \
      "$ip_state_width" "$ip_state" "$connection"
  done
}

ovpn_client_list_plain_command() {
  local id name state entry
  local id_width=9 name_width=4
  local -a entries=()

  ovpn_require_healthy_state
  while IFS=' ' read -r name id state; do
    entries+=("$name"$'\t'"$id"$'\t'"$state")
    if ((${#name} > name_width)); then name_width=${#name}; fi
    if ((${#id} > id_width)); then id_width=${#id}; fi
  done < <(ovpn_client_records)

  if ((${#entries[@]})); then
    printf '%-*s  %-*s  %s\n' "$id_width" 'CLIENT ID' "$name_width" NAME STATE
    for entry in "${entries[@]}"; do
      IFS=$'\t' read -r name id state <<<"$entry"
      printf '%-*s  %-*s  %s\n' "$id_width" "$id" "$name_width" "$name" "$state"
    done
  fi
}

ovpn_client_list_command() {
  case "$#" in
    0)
      ovpn_client_list_plain_command
      ;;
    1)
      [ "$1" = --detail ] || ovpn_die 'usage: ovpn client list [--detail]'
      ovpn_client_list_with_ip_command
      ;;
    *)
      ovpn_die 'usage: ovpn client list [--detail]'
      ;;
  esac
}

ovpn_pki_stage_create() {
  local stage stale

  for stale in "$OVPN_DATA_DIR"/.pki-operation.*; do
    [ -d "$stale" ] || continue
    [ ! -e "$stale/previous" ] || continue
    rm -rf "$stale" || return 1
  done

  stage="$(mktemp -d "$OVPN_DATA_DIR/.pki-operation.XXXXXX")" || return 1
  chmod 700 "$stage" || {
    rm -rf "$stage"
    return 1
  }
  cp -a "$OVPN_DATA_DIR/pki" "$stage/pki" || {
    rm -rf "$stage"
    return 1
  }
  printf '%s\n' "$stage"
}

ovpn_pki_stage_commit() {
  local stage="$1"
  local current="$OVPN_DATA_DIR/pki"
  local previous="$stage/previous"

  [ -d "$stage/pki" ] || return 1
  if ! mv "$current" "$previous"; then
    rm -rf "$stage"
    return 1
  fi
  if ! mv "$stage/pki" "$current"; then
    if mv "$previous" "$current"; then
      rm -rf "$stage"
    else
      ovpn_log "CRITICAL: failed to restore PKI after a staged commit failure; previous PKI remains at $previous"
    fi
    return 1
  fi
  rm -rf "$stage" || ovpn_log "warning: failed to remove committed PKI staging directory: $stage"
}

ovpn_pki_try_revoke_client() {
  local id="$1"
  local bin stage staged_pki

  ovpn_registry_uuid_valid "$id" || return 1
  bin="$(ovpn_easyrsa_bin)" || return 1
  stage="$(ovpn_pki_stage_create)" || return 1
  staged_pki="$stage/pki"
  EASYRSA_BATCH=1 EASYRSA_PKI="$staged_pki" "$bin" revoke "$id" || {
    rm -rf "$stage"
    return 1
  }
  EASYRSA_BATCH=1 EASYRSA_PKI="$staged_pki" "$bin" gen-crl || {
    rm -rf "$stage"
    return 1
  }
  [ -s "$staged_pki/crl.pem" ] || {
    rm -rf "$stage"
    return 1
  }
  chmod 644 "$staged_pki/crl.pem" || {
    rm -rf "$stage"
    return 1
  }
  ovpn_pki_stage_commit "$stage"
}

ovpn_pki_try_issue_client() {
  local id="$1"
  local bin stage staged_pki

  ovpn_registry_uuid_valid "$id" || return 1
  bin="$(ovpn_easyrsa_bin)" || return 1
  stage="$(ovpn_pki_stage_create)" || return 1
  staged_pki="$stage/pki"
  rm -f "$staged_pki/reqs/$id.req" "$staged_pki/private/$id.key" || {
    rm -rf "$stage"
    return 1
  }
  EASYRSA_BATCH=1 EASYRSA_PKI="$staged_pki" EASYRSA_REQ_CN="$id" "$bin" build-client-full "$id" nopass || {
    rm -rf "$stage"
    return 1
  }
  [ -r "$staged_pki/issued/$id.crt" ] || {
    rm -rf "$stage"
    return 1
  }
  [ -r "$staged_pki/private/$id.key" ] || {
    rm -rf "$stage"
    return 1
  }
  chmod 644 "$staged_pki/issued/$id.crt" || {
    rm -rf "$stage"
    return 1
  }
  chmod 600 "$staged_pki/private/$id.key" || {
    rm -rf "$stage"
    return 1
  }
  ovpn_pki_stage_commit "$stage"
}

ovpn_pki_reissue_supported() {
  local id="$1"
  local state="$2"
  local bin probe status=0

  ovpn_registry_uuid_valid "$id" || return 1
  bin="$(ovpn_easyrsa_bin)" || return 1
  probe="$(mktemp -d "$OVPN_DATA_DIR/.reissue-probe.XXXXXX")" || return 1
  if ! cp -a "$OVPN_DATA_DIR/pki" "$probe/pki"; then
    rm -rf "$probe"
    return 1
  fi
  if [ "$state" = active ]; then
    EASYRSA_BATCH=1 EASYRSA_PKI="$probe/pki" "$bin" revoke "$id" >/dev/null 2>&1 || status=1
    if [ "$status" -eq 0 ]; then
      EASYRSA_BATCH=1 EASYRSA_PKI="$probe/pki" "$bin" gen-crl >/dev/null 2>&1 || status=1
    fi
  fi
  if [ "$status" -eq 0 ]; then
    rm -f "$probe/pki/reqs/$id.req" "$probe/pki/private/$id.key"
    EASYRSA_BATCH=1 EASYRSA_PKI="$probe/pki" EASYRSA_REQ_CN="$id" "$bin" build-client-full "$id" nopass >/dev/null 2>&1 || status=1
  fi
  [ -r "$probe/pki/issued/$id.crt" ] && [ -r "$probe/pki/private/$id.key" ] || status=1
  rm -rf "$probe"
  return "$status"
}

ovpn_client_lifecycle_audit() {
  local operation="$1"
  local outcome="$2"
  local id="$3"
  local name="$4"
  local audit_file

  audit_file="$(ovpn_registry_audit_file)"
  printf '{"timestamp":"%s","event":"client_lifecycle","operation":"%s","outcome":"%s","client_id":"%s","client_name":"%s","legacy":false}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$operation" "$outcome" "$id" "$name" >>"$audit_file"
  chmod 600 "$audit_file"
  if declare -F ovpn_event_write >/dev/null 2>&1; then
    ovpn_event_write client_lifecycle "$operation" "$outcome" "$id" "$name" || true
  fi
}

ovpn_client_rename_rewrite_csv() {
  local source="$1"
  local destination="$2"
  local id="$3"
  local new_name="$4"

  awk -F, -v OFS=, -v client_id="$id" -v name="$new_name" '
    $1 == client_id {
      $2 = name
      found++
    }
    { print }
    END { if (found != 1) exit 1 }
  ' "$source" >"$destination"
  chmod 600 "$destination"
}

ovpn_client_rename_rewrite_profile() {
  local source="$1"
  local destination="$2"
  local id="$3"
  local old_name="$4"
  local new_name="$5"

  awk -v client_id="$id" -v old_name="$old_name" -v new_name="$new_name" '
    $0 == "# ovpn-client-id: " client_id {
      ids++
    }
    $0 == "# ovpn-client-name: " old_name {
      print "# ovpn-client-name: " new_name
      names++
      next
    }
    { print }
    END { if (ids != 1 || names != 1) exit 1 }
  ' "$source" >"$destination"
  chmod 600 "$destination"
}

ovpn_client_rename_maybe_fail() {
  [ "${OVPN_CLIENT_RENAME_FAIL_AFTER:-}" != "$1" ] || ovpn_die "injected client rename failure after $1"
}

ovpn_client_rename_audit() {
  local id="$1"
  local old_name="$2"
  local new_name="$3"
  local audit_file

  audit_file="$(ovpn_registry_audit_file)"
  printf '{"timestamp":"%s","event":"client_rename","outcome":"applied","client_id":"%s","client_name":"%s","old_name":"%s","legacy":false}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$id" "$new_name" "$old_name" >>"$audit_file"
  chmod 600 "$audit_file"
}

ovpn_client_rename_inner() (
  local selector_mode="$1"
  local reference="$2"
  local new_name="$3"
  local id old_name state state_file registry audit_file profile_dir old_profile new_profile
  local stage rollback_ok transaction_success=false

  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_resolve_selector_or_die "$selector_mode" "$reference"
  id="$OVPN_CLIENT_RESOLVED_ID"
  old_name="$OVPN_CLIENT_RESOLVED_NAME"
  state="$OVPN_CLIENT_RESOLVED_STATE"
  ovpn_client_name_or_die "$new_name"
  if [ "$old_name" = "$new_name" ]; then
    ovpn_log "client '$old_name' already has that name"
    return 0
  fi
  [ -z "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[$new_name]:-}" ] || ovpn_die "client name already exists: $new_name"

  state_file="$(ovpn_registry_client_state_file)"
  registry="$(ovpn_registry_client_ip_file)"
  audit_file="$(ovpn_registry_audit_file)"
  profile_dir="$OVPN_DATA_DIR/clients/$state"
  old_profile="$profile_dir/$old_name.ovpn"
  new_profile="$profile_dir/$new_name.ovpn"
  [ -r "$old_profile" ] || ovpn_die "client profile is missing: $old_profile"
  [ ! -e "$OVPN_DATA_DIR/clients/active/$new_name.ovpn" ] &&
    [ ! -e "$OVPN_DATA_DIR/clients/revoked/$new_name.ovpn" ] ||
    ovpn_die "client profile path already exists for '$new_name'"

  stage="$(mktemp -d "$OVPN_DATA_DIR/meta/.client-rename.XXXXXX")" ||
    ovpn_die 'failed to create client rename staging directory'
  chmod 700 "$stage"
  if ! cp "$state_file" "$stage/state.original" ||
    ! cp "$registry" "$stage/registry.original" ||
    ! cp "$audit_file" "$stage/audit.original" ||
    ! cp "$old_profile" "$stage/profile.original"; then
    rm -rf "$stage"
    ovpn_die 'failed to back up client rename targets'
  fi

  ovpn_client_rename_cleanup() {
    local status=$?

    trap - EXIT
    set +e
    if [ "$transaction_success" != true ]; then
      rollback_ok=true
      ovpn_client_ip_atomic_install "$stage/state.original" "$state_file" || rollback_ok=false
      ovpn_client_ip_atomic_install "$stage/registry.original" "$registry" || rollback_ok=false
      ovpn_client_ip_atomic_install "$stage/audit.original" "$audit_file" || rollback_ok=false
      rm -f "$new_profile"
      ovpn_client_ip_atomic_install "$stage/profile.original" "$old_profile" || rollback_ok=false
      if [ "$rollback_ok" != true ]; then
        ovpn_log "CRITICAL: client rename rollback was incomplete; recovery files remain at $stage"
        exit "$status"
      fi
    fi
    rm -rf "$stage"
    exit "$status"
  }
  trap ovpn_client_rename_cleanup EXIT

  ovpn_client_rename_rewrite_csv "$state_file" "$stage/state.candidate" "$id" "$new_name"
  ovpn_client_rename_rewrite_csv "$registry" "$stage/registry.candidate" "$id" "$new_name"
  ovpn_client_rename_rewrite_profile "$old_profile" "$stage/profile.candidate" "$id" "$old_name" "$new_name"
  ovpn_registry_load_identities "$stage/state.candidate" ||
    ovpn_die 'renamed identity registry candidate is invalid'

  ovpn_client_ip_atomic_install "$stage/state.candidate" "$state_file"
  ovpn_client_rename_maybe_fail identity
  ovpn_client_ip_validate_file "$stage/registry.candidate" ||
    ovpn_die 'renamed client-IP registry candidate is invalid'
  ovpn_client_ip_write_canonical_file "$stage/registry.canonical"
  ovpn_client_ip_atomic_install "$stage/registry.canonical" "$registry"
  ovpn_client_rename_maybe_fail registries
  ovpn_client_ip_atomic_install "$stage/profile.candidate" "$new_profile"
  rm -f "$old_profile"
  ovpn_client_rename_maybe_fail profile
  ovpn_client_rename_audit "$id" "$old_name" "$new_name"
  ovpn_client_rename_maybe_fail audit

  transaction_success=true
  if declare -F ovpn_event_write >/dev/null 2>&1; then
    ovpn_event_write client_lifecycle rename applied "$id" "$new_name" \
      "$(jq -cn --arg old_name "$old_name" '{old_name:$old_name}')" || true
  fi
  ovpn_log "renamed client '$old_name' to '$new_name' [$id]"
)

ovpn_client_rename_command() {
  local usage='usage: ovpn client rename <client>|--id <ID>|--name <NAME> <new-name>'
  local selector_mode reference consumed new_name

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"
  new_name="${1:-}"
  [ -n "$new_name" ] && [ "$#" -eq 1 ] || ovpn_die "$usage"
  ovpn_with_data_lock client ovpn_client_rename_inner "$selector_mode" "$reference" "$new_name"
}

ovpn_client_lifecycle_kick() {
  local id="$1"

  OVPN_CLIENT_IP_SYNC_CHANGED_CLIENTS=("$id")
  ovpn_client_ip_kick_changed_clients
}

ovpn_client_lifecycle_move_profile_to_revoked() {
  local name="$1"

  mkdir -p "$OVPN_DATA_DIR/clients/revoked"
  if [ -e "$OVPN_DATA_DIR/clients/active/$name.ovpn" ]; then
    mv "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$OVPN_DATA_DIR/clients/revoked/$name.ovpn" || \
      ovpn_log "warning: failed to move client profile for '$name' to revoked directory"
  fi
}

ovpn_client_revoke_inner() {
  local selector_mode="$1"
  local reference="$2"
  local release_ip="$3"
  local name index id assignment

  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_resolve_selector_or_die "$selector_mode" "$reference"
  id="$OVPN_CLIENT_RESOLVED_ID"
  name="$OVPN_CLIENT_RESOLVED_NAME"
  ovpn_client_require_registry_active "$name"
  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  assignment="${OVPN_CLIENT_IP_VALUES[index]}"
  if ! ovpn_pki_try_revoke_client "$id"; then
    ovpn_client_lifecycle_audit revoke failed "$id" "$name" || true
    ovpn_die 'failed to revoke client certificate; no assignment changes were applied'
  fi
  ovpn_client_lifecycle_move_profile_to_revoked "$name"
  ovpn_client_registry_set_state "$name" revoked
  if [ "$release_ip" = true ] && [ -n "$assignment" ]; then
    ovpn_client_ip_set_current_assignment "$name" ''
    ovpn_client_ip_apply_current_mutation
  else
    [ -n "$assignment" ] || ovpn_client_ip_clear_dynamic_lease "$id"
    ovpn_client_lifecycle_kick "$id"
  fi
  ovpn_client_lifecycle_audit revoke applied "$id" "$name" || true
  ovpn_log "revoked client '$name'"
}

ovpn_client_revoke_command() {
  local usage='usage: ovpn client revoke <client>|--id <ID>|--name <NAME> [--release-ip]'
  local selector_mode reference consumed
  local release_ip=false

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"
  if [ "$#" -eq 1 ] && [ "$1" = --release-ip ]; then
    release_ip=true
  elif [ "$#" -ne 0 ]; then
    ovpn_die "$usage"
  fi
  ovpn_with_data_lock client ovpn_client_revoke_inner "$selector_mode" "$reference" "$release_ip"
}

ovpn_client_release_ip_inner() {
  local selector_mode="$1"
  local reference="$2"
  local name status index assignment id

  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_resolve_selector_or_die "$selector_mode" "$reference"
  id="$OVPN_CLIENT_RESOLVED_ID"
  name="$OVPN_CLIENT_RESOLVED_NAME"
  status="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
  [ "$status" = revoked ] || ovpn_die "client $name is not revoked"
  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  assignment="${OVPN_CLIENT_IP_VALUES[index]}"
  [ -n "$assignment" ] || ovpn_die "client $name does not have a static IP reservation"
  ovpn_client_ip_set_current_assignment "$name" ""
  if ! ovpn_client_ip_apply_current_mutation; then
    ovpn_client_lifecycle_audit release_ip failed "$id" "$name" || true
    ovpn_die "failed to release the client IP; the registry was restored"
  fi
  ovpn_client_lifecycle_audit release_ip applied "$id" "$name" || true
  ovpn_log "released static IP for revoked client $name"
}

ovpn_client_release_ip_command() {
  local usage='usage: ovpn client ip release <client>|--id <ID>|--name <NAME>'
  local selector_mode reference consumed

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"
  [ "$#" -eq 0 ] || ovpn_die "$usage"
  ovpn_with_data_lock client ovpn_client_release_ip_inner "$selector_mode" "$reference"
}

ovpn_client_reissue_inner() {
  local selector_mode="$1"
  local reference="$2"
  local mode="$3"
  local requested_ip="$4"
  local name status index id assignment allocated_ip=''

  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_resolve_selector_or_die "$selector_mode" "$reference"
  id="$OVPN_CLIENT_RESOLVED_ID"
  name="$OVPN_CLIENT_RESOLVED_NAME"
  status="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
  [ -n "$status" ] || ovpn_die "client '$name' does not exist"

  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  assignment="${OVPN_CLIENT_IP_VALUES[index]}"

  case "$mode" in
    dynamic)
      [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ] || ovpn_die 'cannot set a client to dynamic: dynamic pool capacity is 0; enlarge the dynamic pool first'
      ;;
    static)
      ovpn_client_ip_require_static_address "$requested_ip" "$name"
      ;;
    '')
      if [ -z "$assignment" ]; then
        allocated_ip="$(ovpn_client_ip_allocate_static)"
      fi
      ;;
    *) ovpn_die "unsupported reissue mode: $mode" ;;
  esac

  if ! ovpn_pki_reissue_supported "$id" "$status"; then
    ovpn_client_lifecycle_audit reissue rejected "$id" "$name" || true
    ovpn_die 'the current Easy-RSA runtime does not support same-CN reissue; no PKI index changes were made'
  fi

  if [ "$status" = active ]; then
    if ! ovpn_pki_try_revoke_client "$id"; then
      ovpn_client_lifecycle_audit reissue failed "$id" "$name" || true
      ovpn_die 'failed to revoke the active certificate before reissue'
    fi
    ovpn_client_lifecycle_move_profile_to_revoked "$name"
    ovpn_client_registry_set_state "$name" revoked
  fi
  if ! ovpn_pki_try_issue_client "$id"; then
    ovpn_client_registry_set_state "$name" revoked
    ovpn_client_lifecycle_audit reissue failed "$id" "$name" || true
    ovpn_die 'client reissue failed; the previous certificate remains revoked and the IP assignment was retained'
  fi
  ovpn_client_registry_set_state "$name" active

  case "$mode" in
    dynamic)
      ovpn_client_ip_set_current_assignment "$name" ''
      ovpn_client_ip_apply_current_mutation
      ;;
    static)
      ovpn_client_ip_set_current_assignment "$name" "$requested_ip"
      ovpn_client_ip_apply_current_mutation
      ;;
    '')
      if [ -n "$allocated_ip" ]; then
        ovpn_client_ip_set_current_assignment "$name" "$allocated_ip"
        ovpn_client_ip_apply_current_mutation
      fi
      ;;
  esac

  ovpn_write_or_print "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$(ovpn_render_client_content "$name")"
  ovpn_client_lifecycle_kick "$id"
  ovpn_client_lifecycle_audit reissue applied "$id" "$name" || true
  ovpn_log "reissued client '$name'"
}

ovpn_client_reissue_command() {
  local usage='usage: ovpn client reissue <client>|--id <ID>|--name <NAME> [--dynamic|--ip <IPv4>]'
  local selector_mode reference consumed
  local mode='' requested_ip=''

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dynamic)
        [ -z "$mode" ] || ovpn_die '--dynamic cannot be combined with --ip'
        mode=dynamic
        ;;
      --ip)
        shift
        [ "$#" -gt 0 ] || ovpn_die '--ip requires an IPv4 address'
        [ -z "$mode" ] || ovpn_die '--ip cannot be combined with --dynamic'
        mode=static
        requested_ip="$1"
        ;;
      *) ovpn_die "$usage" ;;
    esac
    shift
  done
  ovpn_with_data_lock client ovpn_client_reissue_inner "$selector_mode" "$reference" "$mode" "$requested_ip"
}

ovpn_client_delete_current_assignment() {
  local name="$1"
  local index
  local -a ids=()
  local -a names=()
  local -a values=()
  local -a ints=()

  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    [ "${OVPN_CLIENT_IP_NAMES[index]}" = "$name" ] && continue
    ids+=("${OVPN_CLIENT_IP_IDS[index]}")
    names+=("${OVPN_CLIENT_IP_NAMES[index]}")
    values+=("${OVPN_CLIENT_IP_VALUES[index]}")
    ints+=("${OVPN_CLIENT_IP_INTS[index]}")
  done
  OVPN_CLIENT_IP_IDS=("${ids[@]}")
  OVPN_CLIENT_IP_NAMES=("${names[@]}")
  OVPN_CLIENT_IP_VALUES=("${values[@]}")
  OVPN_CLIENT_IP_INTS=("${ints[@]}")
}

ovpn_client_delete_inner() {
  local selector_mode="$1"
  local reference="$2"
  local name status index id state_file state_backup

  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_resolve_selector_or_die "$selector_mode" "$reference"
  id="$OVPN_CLIENT_RESOLVED_ID"
  name="$OVPN_CLIENT_RESOLVED_NAME"
  status="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
  [ -n "$status" ] || ovpn_die "client '$name' does not exist"
  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  if [ "$status" = active ]; then
    if ! ovpn_pki_try_revoke_client "$id"; then
      ovpn_client_lifecycle_audit delete failed "$id" "$name" || true
      ovpn_die 'failed to revoke the active certificate before deletion'
    fi
    ovpn_client_lifecycle_move_profile_to_revoked "$name"
    ovpn_client_registry_set_state "$name" revoked
  fi
  state_file="$(ovpn_registry_client_state_file)"
  state_backup="$(mktemp "$OVPN_DATA_DIR/meta/.client-state.delete.XXXXXX")" || ovpn_die "failed to create backup temp file"
  cp "$state_file" "$state_backup" || ovpn_die "failed to backup client state file"
  ovpn_client_registry_set_state "$name" deleted
  ovpn_client_delete_current_assignment "$name"
  if ! ovpn_client_ip_apply_current_mutation; then
    if cp "$state_backup" "$state_file"; then
      rm -f "$state_backup"
    else
      ovpn_log "CRITICAL: failed to restore client state file; backup preserved at $state_backup"
    fi
    ovpn_client_lifecycle_audit delete failed "$id" "$name" || true
    ovpn_die 'failed to remove the client assignment; the registry was restored'
  fi
  rm -f "$state_backup"
  rm -f "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$OVPN_DATA_DIR/clients/revoked/$name.ovpn"
  rm -f "$OVPN_DATA_DIR/pki/private/$id.key" "$OVPN_DATA_DIR/pki/issued/$id.crt" "$OVPN_DATA_DIR/pki/reqs/$id.req"
  ovpn_client_lifecycle_audit delete applied "$id" "$name" || true
  ovpn_log "deleted client '$name'"
}

ovpn_client_delete_command() {
  local usage='usage: ovpn client delete <client>|--id <ID>|--name <NAME>'
  local selector_mode reference consumed

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"
  [ "$#" -eq 0 ] || ovpn_die "$usage"
  ovpn_with_data_lock client ovpn_client_delete_inner "$selector_mode" "$reference"
}


ovpn_client_command() {
  local subcommand="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_client_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die "usage: ovpn client <create|export|list|rename|revoke|reissue|delete|ip> ..."
  shift
  case "$subcommand" in
    create)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client create <name> [--dynamic|--ip <IPv4>]" "Create a client certificate, profile, and IP assignment."
      else
        ovpn_client_create_command "$@"
      fi
      ;;
    export)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client export <client>|--id <ID>|--name <NAME>" "Render an active client profile to stdout."
      else
        ovpn_client_export_command "$@"
      fi
      ;;
    ip)
      local ip_subcommand="${1:-}"
      if ovpn_help_requested "$@"; then
        ovpn_client_ip_usage
        return 0
      fi
      [ -n "$ip_subcommand" ] || ovpn_die "usage: ovpn client ip <release|set> ..."
      shift
      case "$ip_subcommand" in
        set)
          if ovpn_help_requested "$@"; then
            ovpn_command_usage "ovpn client ip set <client...>|--id <ID>|--name <NAME>|--all [--dynamic|--ip <IPv4>]" "Assign client IP addresses."
          else
            ovpn_client_set_command "$@"
          fi
          ;;
        release)
          if ovpn_help_requested "$@"; then
            ovpn_command_usage "ovpn client ip release <client>|--id <ID>|--name <NAME>" "Release the retained static IP of a revoked client."
          else
            ovpn_client_release_ip_command "$@"
          fi
          ;;
        *) ovpn_die "usage: ovpn client ip <release|set> ..." ;;
      esac
      ;;
    revoke)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client revoke <client>|--id <ID>|--name <NAME> [--release-ip]" "Revoke a client certificate and optionally release its static IP."
      else
        ovpn_client_revoke_command "$@"
      fi
      ;;
    reissue)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client reissue <client>|--id <ID>|--name <NAME> [--dynamic|--ip <IPv4>]" "Issue a new certificate for an existing client, optionally changing IP assignment."
      else
        ovpn_client_reissue_command "$@"
      fi
      ;;
    delete)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client delete <client>|--id <ID>|--name <NAME>" "Remove a client and its local credentials."
      else
        ovpn_client_delete_command "$@"
      fi
      ;;
    list)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client list [--detail]" "List client certificate state and optional detailed IP assignment."
      else
        ovpn_client_list_command "$@"
      fi
      ;;
    rename)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client rename <client>|--id <ID>|--name <NAME> <new-name>" "Change a client's display name without replacing its UUID or certificate."
      else
        ovpn_client_rename_command "$@"
      fi
      ;;
    *) ovpn_die "usage: ovpn client <create|export|list|rename|revoke|reissue|delete|ip> ..." ;;
  esac
}
