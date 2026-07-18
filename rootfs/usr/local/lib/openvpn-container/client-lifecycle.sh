#!/usr/bin/env bash

ovpn_client_records() {
  local name

  ovpn_client_ip_collect_pki_clients || return 1
  for name in "${!OVPN_CLIENT_IP_PKI_STATES[@]}"; do
    printf '%s %s\n' "$name" "${OVPN_CLIENT_IP_PKI_STATES[$name]}"
  done | LC_ALL=C sort
}

declare -A OVPN_CLIENT_LIST_CONNECTED_IPS=()
declare -A OVPN_CLIENT_LIST_PERSISTED_IPS=()
OVPN_CLIENT_LIST_MANAGEMENT_AVAILABLE=false

ovpn_client_list_prepare_applied_registry() {
  local applied

  applied="$(ovpn_registry_applied_file)"
  [ -r "$applied" ] || ovpn_die "cannot read applied client-IP registry: $applied"
  ovpn_client_ip_validate_file "$applied" || ovpn_die 'applied client-IP registry is invalid; restore it before listing IPs'
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
  local index name state assignment address ip_state connection mode row
  local client_width=6 state_width=5 mode_width=4 ip_width=2 ip_state_width=8
  local -a rows=()

  ovpn_require_healthy_state
  ovpn_client_list_prepare_applied_registry
  ovpn_client_list_load_persisted_dynamic_ips
  ovpn_client_list_load_connected_clients
  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
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
    rows+=("$name"$'\t'"$state"$'\t'"$mode"$'\t'"$address"$'\t'"$ip_state"$'\t'"$connection")
  done

  if ((${#rows[@]})); then
    mapfile -t rows < <(printf '%s\n' "${rows[@]}" | LC_ALL=C sort)
  fi
  for row in "${rows[@]}"; do
    IFS=$'\t' read -r name state mode address ip_state connection <<<"$row"
    if ((${#name} > client_width)); then client_width=${#name}; fi
    if ((${#state} > state_width)); then state_width=${#state}; fi
    if ((${#mode} > mode_width)); then mode_width=${#mode}; fi
    if ((${#address} > ip_width)); then ip_width=${#address}; fi
    if ((${#ip_state} > ip_state_width)); then ip_state_width=${#ip_state}; fi
  done
  printf '%-*s  %-*s  %-*s  %-*s  %-*s  %s\n' \
    "$client_width" CLIENT "$state_width" STATE "$mode_width" MODE "$ip_width" IP \
    "$ip_state_width" 'IP STATE' CONNECTION
  for row in "${rows[@]}"; do
    IFS=$'\t' read -r name state mode address ip_state connection <<<"$row"
    printf '%-*s  %-*s  %-*s  %-*s  %-*s  %s\n' \
      "$client_width" "$name" "$state_width" "$state" "$mode_width" "$mode" "$ip_width" "$address" \
      "$ip_state_width" "$ip_state" "$connection"
  done
}

ovpn_client_list_plain_command() {
  local name state
  local name_width=6
  local -a entries=()

  ovpn_require_healthy_state
  while IFS=' ' read -r name state; do
    entries+=("$name"$'\t'"$state")
    if ((${#name} > name_width)); then name_width=${#name}; fi
  done < <(ovpn_client_records)

  if ((${#entries[@]})); then
    printf '%-*s  %s\n' "$name_width" CLIENT STATE
    for entry in "${entries[@]}"; do
      IFS=$'\t' read -r name state <<<"$entry"
      printf '%-*s  %s\n' "$name_width" "$name" "$state"
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

ovpn_pki_try_revoke_client() {
  local id="$1"
  local bin

  ovpn_registry_uuid_valid "$id" || return 1
  bin="$(ovpn_easyrsa_bin)" || return 1
  EASYRSA_BATCH=1 EASYRSA_PKI="$OVPN_DATA_DIR/pki" "$bin" revoke "$id"
  EASYRSA_BATCH=1 EASYRSA_PKI="$OVPN_DATA_DIR/pki" "$bin" gen-crl
  [ -s "$OVPN_DATA_DIR/pki/crl.pem" ] || return 1
  chmod 644 "$OVPN_DATA_DIR/pki/crl.pem"
}

ovpn_pki_try_issue_client() {
  local id="$1"
  local bin

  ovpn_registry_uuid_valid "$id" || return 1
  bin="$(ovpn_easyrsa_bin)" || return 1
  rm -f "$OVPN_DATA_DIR/pki/reqs/$id.req" "$OVPN_DATA_DIR/pki/private/$id.key"
  EASYRSA_BATCH=1 EASYRSA_PKI="$OVPN_DATA_DIR/pki" EASYRSA_REQ_CN="$id" "$bin" build-client-full "$id" nopass
  [ -r "$OVPN_DATA_DIR/pki/issued/$id.crt" ] || return 1
  [ -r "$OVPN_DATA_DIR/pki/private/$id.key" ] || return 1
  chmod 644 "$OVPN_DATA_DIR/pki/issued/$id.crt"
  chmod 600 "$OVPN_DATA_DIR/pki/private/$id.key"
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
  local result="$2"
  local audit_file

  audit_file="$(ovpn_registry_audit_file)"
  printf '{"timestamp":"%s","operation":"%s","result":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$operation" "$result" >>"$audit_file"
  chmod 600 "$audit_file"
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
  local name="$1"
  local release_ip="$2"
  local index id assignment

  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_require_registry_active "$name"
  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  id="${OVPN_CLIENT_IP_IDS[index]}"
  assignment="${OVPN_CLIENT_IP_VALUES[index]}"
  if ! ovpn_pki_try_revoke_client "$id"; then
    ovpn_client_lifecycle_audit revoke failed || true
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
  ovpn_client_lifecycle_audit revoke applied || true
  ovpn_log "revoked client '$name'"
}

ovpn_client_revoke_command() {
  local name="${1:-}"
  local release_ip=false

  [ -n "$name" ] || ovpn_die 'usage: ovpn client revoke <name> [--release-ip]'
  shift
  if [ "$#" -eq 1 ] && [ "$1" = --release-ip ]; then
    release_ip=true
  elif [ "$#" -ne 0 ]; then
    ovpn_die 'usage: ovpn client revoke <name> [--release-ip]'
  fi
  ovpn_with_data_lock client ovpn_client_revoke_inner "$name" "$release_ip"
}

ovpn_client_release_ip_inner() {
  local name="$1"
  local status index assignment

  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  status="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
  [ "$status" = revoked ] || ovpn_die "client $name is not revoked"
  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  assignment="${OVPN_CLIENT_IP_VALUES[index]}"
  [ -n "$assignment" ] || ovpn_die "client $name does not have a static IP reservation"
  ovpn_client_ip_set_current_assignment "$name" ""
  if ! ovpn_client_ip_apply_current_mutation; then
    ovpn_client_lifecycle_audit release_ip failed || true
    ovpn_die "failed to release the client IP; the registry was restored"
  fi
  ovpn_client_lifecycle_audit release_ip applied || true
  ovpn_log "released static IP for revoked client $name"
}

ovpn_client_release_ip_command() {
  local name="${1:-}"

  [ -n "$name" ] || ovpn_die "usage: ovpn client ip release <name>"
  [ "$#" -eq 1 ] || ovpn_die "usage: ovpn client ip release <name>"
  ovpn_with_data_lock client ovpn_client_release_ip_inner "$name"
}

ovpn_client_reissue_inner() {
  local name="$1"
  local mode="$2"
  local requested_ip="$3"
  local status index id assignment allocated_ip=''

  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  status="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
  [ -n "$status" ] || ovpn_die "client '$name' does not exist"

  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  id="${OVPN_CLIENT_IP_IDS[index]}"
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
    ovpn_client_lifecycle_audit reissue rejected || true
    ovpn_die 'the current Easy-RSA runtime does not support same-CN reissue; no PKI index changes were made'
  fi

  if [ "$status" = active ]; then
    if ! ovpn_pki_try_revoke_client "$id"; then
      ovpn_client_lifecycle_audit reissue failed || true
      ovpn_die 'failed to revoke the active certificate before reissue'
    fi
    ovpn_client_lifecycle_move_profile_to_revoked "$name"
    ovpn_client_registry_set_state "$name" revoked
  fi
  if ! ovpn_pki_try_issue_client "$id"; then
    ovpn_client_registry_set_state "$name" revoked
    ovpn_client_lifecycle_audit reissue failed || true
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

  ovpn_render_client "$name" --output "$OVPN_DATA_DIR/clients/active/$name.ovpn"
  ovpn_client_lifecycle_kick "$id"
  ovpn_client_lifecycle_audit reissue applied || true
  ovpn_log "reissued client '$name'"
}

ovpn_client_reissue_command() {
  local name="${1:-}"
  local mode='' requested_ip=''

  [ -n "$name" ] || ovpn_die 'usage: ovpn client reissue <name> [--dynamic|--ip <IPv4>]'
  shift
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
      *) ovpn_die 'usage: ovpn client reissue <name> [--dynamic|--ip <IPv4>]' ;;
    esac
    shift
  done
  ovpn_with_data_lock client ovpn_client_reissue_inner "$name" "$mode" "$requested_ip"
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
  local name="$1"
  local status index id state_file state_backup

  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  status="${OVPN_CLIENT_IP_PKI_STATES[$name]:-}"
  [ -n "$status" ] || ovpn_die "client '$name' does not exist"
  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the in-memory registry"
  id="${OVPN_CLIENT_IP_IDS[index]}"
  if [ "$status" = active ]; then
    if ! ovpn_pki_try_revoke_client "$id"; then
      ovpn_client_lifecycle_audit delete failed || true
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
    ovpn_client_lifecycle_audit delete failed || true
    ovpn_die 'failed to remove the client assignment; the registry was restored'
  fi
  rm -f "$state_backup"
  rm -f "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$OVPN_DATA_DIR/clients/revoked/$name.ovpn"
  rm -f "$OVPN_DATA_DIR/pki/private/$id.key" "$OVPN_DATA_DIR/pki/issued/$id.crt" "$OVPN_DATA_DIR/pki/reqs/$id.req"
  ovpn_client_lifecycle_audit delete applied || true
  ovpn_log "deleted client '$name'"
}

ovpn_client_delete_command() {
  local name="${1:-}"

  [ -n "$name" ] || ovpn_die 'usage: ovpn client delete <name>'
  [ "$#" -eq 1 ] || ovpn_die 'usage: ovpn client delete <name>'
  ovpn_with_data_lock client ovpn_client_delete_inner "$name"
}


ovpn_client_command() {
  local subcommand="${1:-}"

  if ovpn_help_requested "$@"; then
    ovpn_client_usage
    return 0
  fi
  [ -n "$subcommand" ] || ovpn_die "usage: ovpn client <create|export|list|revoke|reissue|delete|ip> ..."
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
        ovpn_command_usage "ovpn client export <name>" "Render an active client profile to stdout."
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
            ovpn_command_usage "ovpn client ip set <client...|--all> [--dynamic|--ip <IPv4>]" "Assign client IP addresses."
          else
            ovpn_client_set_command "$@"
          fi
          ;;
        release)
          if ovpn_help_requested "$@"; then
            ovpn_command_usage "ovpn client ip release <name>" "Release the retained static IP of a revoked client."
          else
            ovpn_client_release_ip_command "$@"
          fi
          ;;
        *) ovpn_die "usage: ovpn client ip <release|set> ..." ;;
      esac
      ;;
    revoke)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client revoke <name> [--release-ip]" "Revoke a client certificate and optionally release its static IP."
      else
        ovpn_client_revoke_command "$@"
      fi
      ;;
    reissue)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client reissue <name> [--dynamic|--ip <IPv4>]" "Issue a new certificate for an existing client, optionally changing IP assignment."
      else
        ovpn_client_reissue_command "$@"
      fi
      ;;
    delete)
      if ovpn_help_requested "$@"; then
        ovpn_command_usage "ovpn client delete <name>" "Remove a client and its local credentials."
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
    *) ovpn_die "usage: ovpn client <create|export|list|revoke|reissue|delete|ip> ..." ;;
  esac
}
