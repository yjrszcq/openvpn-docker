#!/usr/bin/env bash

OVPN_CLIENT_MUTATION_TARGETS=()

ovpn_client_ip_require_applied_draft() {
  local draft applied

  draft="$(ovpn_registry_client_ip_file)"
  applied="$(ovpn_registry_applied_file)"
  [ -r "$draft" ] && [ -r "$applied" ] || ovpn_die 'client-IP registry is unavailable; restore the V2 registry first'
  cmp -s "$draft" "$applied" || ovpn_die 'client-IP draft is waiting for explicit application; run: ovpn client-ip apply'
}

ovpn_client_ip_prepare_mutation() {
  local applied

  ovpn_client_ip_require_applied_draft
  applied="$(ovpn_registry_applied_file)"
  ovpn_client_ip_validate_file "$applied" || ovpn_die 'applied client-IP registry is invalid; restore it before changing clients'
}

ovpn_client_ip_assignment_index() {
  local wanted="$1"
  local index

  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    if [ "${OVPN_CLIENT_IP_NAMES[index]}" = "$wanted" ]; then
      printf '%s\n' "$index"
      return 0
    fi
  done
  return 1
}

ovpn_client_ip_set_current_assignment() {
  local name="$1"
  local value="$2"
  local index

  index="$(ovpn_client_ip_assignment_index "$name")" || ovpn_die "client '$name' is missing from the applied client-IP registry"
  OVPN_CLIENT_IP_VALUES[index]="$value"
  if [ -n "$value" ]; then
    OVPN_CLIENT_IP_INTS[index]="$(ovpn_ipam_ipv4_to_int "$value")"
  else
    OVPN_CLIENT_IP_INTS[index]=''
  fi
}

ovpn_client_ip_static_ip_available() {
  local address="$1"
  local exclude_name="${2:-}"
  local index

  for ((index = 0; index < ${#OVPN_CLIENT_IP_NAMES[@]}; index++)); do
    [ "${OVPN_CLIENT_IP_NAMES[index]}" = "$exclude_name" ] && continue
    [ "${OVPN_CLIENT_IP_VALUES[index]}" = "$address" ] && return 1
  done
  return 0
}

ovpn_client_ip_require_static_address() {
  local address="$1"
  local exclude_name="${2:-}"

  [ "$OVPN_IPAM_STATIC_CAPACITY" -gt 0 ] || ovpn_die 'cannot allocate a static IP: static capacity is 0; shrink the dynamic pool or expand the VPN network'
  ovpn_ipam_ipv4_to_int "$address" >/dev/null || ovpn_die "invalid static IP: $address"
  ovpn_ipam_ip_in_static_range "$address" || ovpn_die "static IP '$address' is outside the static address region"
  ovpn_client_ip_static_ip_available "$address" "$exclude_name" || ovpn_die "static IP '$address' is already assigned"
}

ovpn_client_ip_allocate_static() {
  local candidate address
  local -A used=()
  local index

  [ "$OVPN_IPAM_STATIC_CAPACITY" -gt 0 ] || ovpn_die 'cannot allocate a static IP: static capacity is 0; shrink the dynamic pool or expand the VPN network'
  for ((index = 0; index < ${#OVPN_CLIENT_IP_VALUES[@]}; index++)); do
    [ -n "${OVPN_CLIENT_IP_VALUES[index]}" ] || continue
    used["${OVPN_CLIENT_IP_INTS[index]}"]=1
  done
  for ((candidate = OVPN_IPAM_STATIC_START_INT; candidate <= OVPN_IPAM_STATIC_END_INT; candidate++)); do
    if [ -z "${used[$candidate]+present}" ]; then
      address="$(ovpn_ipam_int_to_ipv4 "$candidate")"
      printf '%s\n' "$address"
      return 0
    fi
  done
  ovpn_die 'cannot allocate a static IP: the static address region is full'
}

ovpn_client_ip_write_current_draft() {
  local draft candidate

  draft="$(ovpn_registry_client_ip_file)"
  candidate="${draft}.mutation.$$"
  ovpn_client_ip_write_canonical_file "$candidate"
  ovpn_client_ip_atomic_install "$candidate" "$draft"
  rm -f "$candidate"
}

ovpn_client_ip_apply_current_mutation() {
  ovpn_client_ip_write_current_draft
  ovpn_client_ip_apply_inner
}

ovpn_client_registry_set_state() {
  local name="$1"
  local state="$2"
  local state_file temporary line current_name current_state

  state_file="$(ovpn_registry_client_state_file)"
  temporary="${state_file}.mutation.$$"
  {
    printf '%s\n' '# client,state'
    {
      if [ -r "$state_file" ]; then
        while IFS= read -r line || [ -n "$line" ]; do
          [ "$line" = '# client,state' ] && continue
          current_name="${line%%,*}"
          current_state="${line#*,}"
          [ "$current_name" = "$name" ] && continue
          [ -n "$current_name" ] || continue
          printf '%s,%s\n' "$current_name" "$current_state"
        done <"$state_file"
      fi
      printf '%s,%s\n' "$name" "$state"
    } | LC_ALL=C sort
  } >"$temporary"
  mv "$temporary" "$state_file"
  chmod 600 "$state_file"
}

ovpn_client_require_registry_active() {
  local name="$1"

  [ "${OVPN_CLIENT_IP_PKI_STATES[$name]:-}" = active ] || ovpn_die "client '$name' is not active"
}

ovpn_client_create_inner() {
  local name="$1"
  local mode="$2"
  local requested_ip="$3"
  local assignment

  ovpn_client_name_or_die "$name"
  ovpn_require_healthy_state
  ovpn_client_ip_prepare_mutation
  ovpn_client_refuse_duplicate "$name"
  case "$mode" in
    dynamic)
      [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ] || ovpn_die 'cannot create a dynamic client: dynamic pool capacity is 0; enlarge the dynamic pool first'
      assignment=''
      ;;
    static)
      if [ -n "$requested_ip" ]; then
        ovpn_client_ip_require_static_address "$requested_ip"
        assignment="$requested_ip"
      else
        assignment="$(ovpn_client_ip_allocate_static)"
      fi
      ;;
    *) ovpn_die "unsupported client assignment mode: $mode" ;;
  esac

  ovpn_pki_issue_client "$name"
  OVPN_CLIENT_IP_NAMES+=("$name")
  OVPN_CLIENT_IP_VALUES+=("$assignment")
  if [ -n "$assignment" ]; then
    OVPN_CLIENT_IP_INTS+=("$(ovpn_ipam_ipv4_to_int "$assignment")")
  else
    OVPN_CLIENT_IP_INTS+=('')
  fi
  ovpn_client_registry_set_state "$name" active
  ovpn_client_ip_apply_current_mutation
  ovpn_render_client "$name" --output "$OVPN_DATA_DIR/clients/active/$name.ovpn"
  ovpn_log "added client '$name'"
}

ovpn_client_create_command() {
  local name="${1:-}"
  local mode=static requested_ip=''

  [ -n "$name" ] || ovpn_die 'usage: ovpn client create <name> [--dynamic|--ip <IPv4>]'
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dynamic)
        [ "$mode" = static ] && [ -z "$requested_ip" ] || ovpn_die '--dynamic cannot be combined with --ip'
        mode=dynamic
        ;;
      --ip)
        shift
        [ "$#" -gt 0 ] || ovpn_die '--ip requires an IPv4 address'
        [ "$mode" = static ] && [ -z "$requested_ip" ] || ovpn_die '--ip cannot be combined with --dynamic or repeated'
        requested_ip="$1"
        ;;
      *) ovpn_die 'usage: ovpn client create <name> [--dynamic|--ip <IPv4>]' ;;
    esac
    shift
  done
  ovpn_with_data_lock client ovpn_client_create_inner "$name" "$mode" "$requested_ip"
}

ovpn_client_collect_active_targets() {
  local name

  OVPN_CLIENT_MUTATION_TARGETS=()
  for name in "${!OVPN_CLIENT_IP_PKI_STATES[@]}"; do
    [ "${OVPN_CLIENT_IP_PKI_STATES[$name]}" = active ] || continue
    OVPN_CLIENT_MUTATION_TARGETS+=("$name")
  done
  [ "${#OVPN_CLIENT_MUTATION_TARGETS[@]}" -gt 0 ] || ovpn_die 'there are no active clients to change'
}

ovpn_client_collect_named_targets() {
  local name
  local -A seen=()

  OVPN_CLIENT_MUTATION_TARGETS=()
  for name in "$@"; do
    ovpn_client_name_or_die "$name"
    [ -z "${seen[$name]+present}" ] || ovpn_die "client '$name' was specified more than once"
    seen["$name"]=1
    ovpn_client_require_registry_active "$name"
    OVPN_CLIENT_MUTATION_TARGETS+=("$name")
  done
}

ovpn_client_set_dynamic_inner() {
  local use_all="$1"
  shift
  local name

  ovpn_client_ip_prepare_mutation
  [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ] || ovpn_die 'cannot set a client to dynamic: dynamic pool capacity is 0; enlarge the dynamic pool first'
  if [ "$use_all" = true ]; then
    ovpn_client_collect_active_targets
  else
    ovpn_client_collect_named_targets "$@"
  fi
  for name in "${OVPN_CLIENT_MUTATION_TARGETS[@]}"; do
    ovpn_client_ip_set_current_assignment "$name" ''
  done
  ovpn_client_ip_apply_current_mutation
  ovpn_log 'set selected clients to dynamic assignments'
}

ovpn_client_set_dynamic_command() {
  [ "$#" -gt 0 ] || ovpn_die 'usage: ovpn client set-dynamic <client...|--all>'
  if [ "$1" = --all ]; then
    [ "$#" -eq 1 ] || ovpn_die 'usage: ovpn client set-dynamic <client...|--all>'
    ovpn_with_data_lock client ovpn_client_set_dynamic_inner true
  else
    ovpn_with_data_lock client ovpn_client_set_dynamic_inner false "$@"
  fi
}

ovpn_client_editor() {
  local file="$1"
  local editor="${OVPN_EDITOR:-${EDITOR:-nano}}"

  case "$editor" in
    *[[:space:]]*) ovpn_die 'OVPN_EDITOR must be a single executable path' ;;
  esac
  command -v "$editor" >/dev/null 2>&1 || ovpn_die "editor is not available: $editor"
  "$editor" "$file"
}

ovpn_client_set_static_from_editor() {
  local require_all_static="$1"
  local temporary line name value normalized index address
  local -A targets=()
  local -A requests=()
  local -A seen=()

  temporary="$(mktemp "$OVPN_DATA_DIR/data/.client-static.XXXXXX")"
  umask 077
  {
    printf '%s\n' '# client,ip'
    for name in "${OVPN_CLIENT_MUTATION_TARGETS[@]}"; do
      index="$(ovpn_client_ip_assignment_index "$name")"
      value="${OVPN_CLIENT_IP_VALUES[index]}"
      if [ -z "$value" ]; then
        value=auto
      fi
      printf '%s,%s\n' "$name" "$value"
      targets["$name"]=1
    done
  } >"$temporary"
  ovpn_client_editor "$temporary"
  while IFS= read -r line || [ -n "$line" ]; do
    [ "$line" = '# client,ip' ] && continue
    [ -n "$line" ] || ovpn_die 'temporary client assignment editor contains an empty line'
    if [[ "$line" != *,* ]] || [[ "$line" == *,*,* ]]; then
      ovpn_die 'temporary client assignment editor requires client,ip rows'
    fi
    [[ "$line" =~ [[:space:]] ]] && ovpn_die 'temporary client assignment editor does not allow whitespace'
    name="${line%%,*}"
    value="${line#*,}"
    [ -n "${targets[$name]+present}" ] || ovpn_die "temporary client assignment editor contains an unselected client '$name'"
    [ -z "${seen[$name]+present}" ] || ovpn_die "temporary client assignment editor duplicates client '$name'"
    seen["$name"]=1
    normalized="${value,,}"
    case "$normalized" in
      auto|'') ;;
      *) ovpn_client_ip_require_static_address "$value" "$name" ;;
    esac
    requests["$name"]="$normalized"
  done <"$temporary"
  rm -f "$temporary"
  for name in "${OVPN_CLIENT_MUTATION_TARGETS[@]}"; do
    [ -n "${seen[$name]+present}" ] || ovpn_die "temporary client assignment editor is missing client '$name'"
    if [ "$require_all_static" = true ] && [ -z "${requests[$name]}" ]; then
      ovpn_die 'set-static --all cannot leave a client dynamic'
    fi
    ovpn_client_ip_set_current_assignment "$name" ''
  done
  for name in "${OVPN_CLIENT_MUTATION_TARGETS[@]}"; do
    value="${requests[$name]}"
    case "$value" in
      auto)
        address="$(ovpn_client_ip_allocate_static)"
        ovpn_client_ip_set_current_assignment "$name" "$address"
        ;;
      '')
        [ "$OVPN_IPAM_DYNAMIC_POOL_SIZE" -gt 0 ] || ovpn_die 'cannot leave a client dynamic: dynamic pool capacity is 0'
        ;;
      *)
        ovpn_client_ip_require_static_address "$value" "$name"
        ovpn_client_ip_set_current_assignment "$name" "$value"
        ;;
    esac
  done
}

ovpn_client_set_static_inner() {
  local use_all="$1"
  local requested_ip="$2"
  shift 2
  local name address

  ovpn_client_ip_prepare_mutation
  [ "$OVPN_IPAM_STATIC_CAPACITY" -gt 0 ] || ovpn_die 'cannot allocate a static IP: static capacity is 0; shrink the dynamic pool or expand the VPN network'
  if [ "$use_all" = true ]; then
    ovpn_client_collect_active_targets
  else
    ovpn_client_collect_named_targets "$@"
  fi
  if [ "$use_all" = true ] || [ "${#OVPN_CLIENT_MUTATION_TARGETS[@]}" -gt 1 ]; then
    ovpn_client_set_static_from_editor "$use_all"
  else
    name="${OVPN_CLIENT_MUTATION_TARGETS[0]}"
    ovpn_client_ip_set_current_assignment "$name" ''
    if [ -n "$requested_ip" ]; then
      ovpn_client_ip_require_static_address "$requested_ip" "$name"
      address="$requested_ip"
    else
      address="$(ovpn_client_ip_allocate_static)"
    fi
    ovpn_client_ip_set_current_assignment "$name" "$address"
  fi
  ovpn_client_ip_apply_current_mutation
  ovpn_log 'set selected clients to static assignments'
}

ovpn_client_set_static_command() {
  local requested_ip=''
  local -a names=()

  [ "$#" -gt 0 ] || ovpn_die 'usage: ovpn client set-static <client...|--all> [--ip <IPv4>]'
  if [ "$1" = --all ]; then
    [ "$#" -eq 1 ] || ovpn_die 'usage: ovpn client set-static <client...|--all> [--ip <IPv4>]'
    ovpn_with_data_lock client ovpn_client_set_static_inner true ''
    return 0
  fi
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --ip)
        shift
        [ "$#" -gt 0 ] || ovpn_die '--ip requires an IPv4 address'
        [ -z "$requested_ip" ] || ovpn_die '--ip may only be specified once'
        requested_ip="$1"
        ;;
      --*) ovpn_die 'usage: ovpn client set-static <client...|--all> [--ip <IPv4>]' ;;
      *) names+=("$1") ;;
    esac
    shift
  done
  [ "${#names[@]}" -gt 0 ] || ovpn_die 'usage: ovpn client set-static <client...|--all> [--ip <IPv4>]'
  if [ -n "$requested_ip" ] && [ "${#names[@]}" -ne 1 ]; then
    ovpn_die '--ip requires exactly one client'
  fi
  ovpn_with_data_lock client ovpn_client_set_static_inner false "$requested_ip" "${names[@]}"
}

ovpn_client_command() {
  local subcommand="${1:-}"
  [ -n "$subcommand" ] || ovpn_die 'usage: ovpn client <create|set-static|set-dynamic|list> ...'
  shift
  case "$subcommand" in
    create) ovpn_client_create_command "$@" ;;
    set-static) ovpn_client_set_static_command "$@" ;;
    set-dynamic) ovpn_client_set_dynamic_command "$@" ;;
    list) [ "$#" -eq 0 ] || ovpn_die 'usage: ovpn client list'; ovpn_list_clients_command ;;
    *) ovpn_die 'usage: ovpn client <create|set-static|set-dynamic|list> ...' ;;
  esac
}

ovpn_add_client_inner() {
  ovpn_client_create_inner "$1" "$2" "$3"
}

ovpn_add_client_command() {
  ovpn_client_create_command "$@"
}
