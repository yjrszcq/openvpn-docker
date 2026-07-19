#!/usr/bin/env bash

OVPN_CLIENT_RESOLVED_ID=''
OVPN_CLIENT_RESOLVED_NAME=''
OVPN_CLIENT_RESOLVED_STATE=''
OVPN_CLIENT_SELECTOR_MODE=''
OVPN_CLIENT_SELECTOR_REFERENCE=''
OVPN_CLIENT_SELECTOR_CONSUMED=0

ovpn_client_name_or_die() {
  local name="$1"

  if ! ovpn_registry_client_name_valid "$name"; then
    ovpn_die "invalid client name: $name"
  fi
}

ovpn_client_parse_single_selector_or_die() {
  local usage="$1"
  shift

  OVPN_CLIENT_SELECTOR_MODE=''
  OVPN_CLIENT_SELECTOR_REFERENCE=''
  OVPN_CLIENT_SELECTOR_CONSUMED=0
  [ "$#" -gt 0 ] || ovpn_die "$usage"
  case "$1" in
    --id|-i)
      [ "$#" -gt 1 ] || ovpn_die "$1 requires a client ID"
      OVPN_CLIENT_SELECTOR_MODE=id
      OVPN_CLIENT_SELECTOR_REFERENCE="$2"
      OVPN_CLIENT_SELECTOR_CONSUMED=2
      ;;
    --name|-n)
      [ "$#" -gt 1 ] || ovpn_die "$1 requires a client name"
      OVPN_CLIENT_SELECTOR_MODE=name
      OVPN_CLIENT_SELECTOR_REFERENCE="$2"
      OVPN_CLIENT_SELECTOR_CONSUMED=2
      ;;
    --*) ovpn_die "$usage" ;;
    *)
      OVPN_CLIENT_SELECTOR_MODE=auto
      OVPN_CLIENT_SELECTOR_REFERENCE="$1"
      OVPN_CLIENT_SELECTOR_CONSUMED=1
      ;;
  esac
}

ovpn_client_resolve_selector_or_die() {
  local mode="$1"
  local reference="$2"
  local resolved status=0

  case "$mode" in
    id) resolved="$(ovpn_registry_resolve_current_by_id "$reference")" || status=$? ;;
    name) resolved="$(ovpn_registry_resolve_current_by_name "$reference")" || status=$? ;;
    auto) resolved="$(ovpn_registry_resolve_current "$reference")" || status=$? ;;
    *) ovpn_die "invalid client selector mode: $mode" ;;
  esac
  if [ "$status" -ne 0 ]; then
    case "$status:$mode" in
      "$OVPN_REGISTRY_RESOLVE_INVALID:id")
        ovpn_die "invalid client ID '$reference': use 8-32 hexadecimal characters or a full UUID"
        ;;
      "$OVPN_REGISTRY_RESOLVE_INVALID:name") ovpn_die "invalid client name: $reference" ;;
      "$OVPN_REGISTRY_RESOLVE_INVALID:auto") ovpn_die "invalid client reference: $reference" ;;
      "$OVPN_REGISTRY_RESOLVE_AMBIGUOUS:id") ovpn_die "client ID '$reference' is ambiguous; use a longer prefix" ;;
      "$OVPN_REGISTRY_RESOLVE_AMBIGUOUS:auto") ovpn_die "client reference '$reference' is ambiguous; use --id or --name" ;;
      "$OVPN_REGISTRY_RESOLVE_NOT_FOUND:id") ovpn_die "client ID '$reference' does not exist" ;;
      "$OVPN_REGISTRY_RESOLVE_NOT_FOUND:name") ovpn_die "client name '$reference' does not exist" ;;
      "$OVPN_REGISTRY_RESOLVE_NOT_FOUND:auto") ovpn_die "client '$reference' does not exist" ;;
      *) ovpn_die 'failed to read the client identity registry' ;;
    esac
  fi
  IFS=, read -r OVPN_CLIENT_RESOLVED_ID OVPN_CLIENT_RESOLVED_NAME OVPN_CLIENT_RESOLVED_STATE <<<"$resolved"
}

ovpn_client_status() {
  local wanted="$1"
  local name id state
  while read -r name id state; do
    if [ "$name" = "$wanted" ]; then
      printf '%s\n' "$state"
      return 0
    fi
  done < <(ovpn_client_records)
  return 1
}

ovpn_client_require_active() {
  local name="$1"
  local status
  status="$(ovpn_client_status "$name" || true)"
  if [ "$status" != active ]; then
    if [ -n "$status" ]; then
      ovpn_die "client '$name' is $status"
    fi
    ovpn_die "client '$name' does not exist"
  fi
}

ovpn_client_refuse_duplicate() {
  local name="$1"
  local status
  status="$(ovpn_client_status "$name" || true)"
  if [ -n "$status" ]; then
    ovpn_die "client '$name' already exists with status $status"
  fi
}

ovpn_client_export_inner() {
  local selector_mode="$1"
  local reference="$2"
  local name
  local profile

  ovpn_require_healthy_state
  ovpn_client_resolve_selector_or_die "$selector_mode" "$reference"
  name="$OVPN_CLIENT_RESOLVED_NAME"
  ovpn_client_require_active "$name"
  profile="$(ovpn_render_client_content "$name")"
  ovpn_write_or_print "$OVPN_DATA_DIR/clients/active/$name.ovpn" "$profile"
  ovpn_write_or_print - "$profile"
}

ovpn_client_export_command() {
  local usage='usage: ovpn client export <client>|--id|-i <ID>|--name|-n <NAME>'
  local selector_mode reference consumed

  ovpn_client_parse_single_selector_or_die "$usage" "$@"
  selector_mode="$OVPN_CLIENT_SELECTOR_MODE"
  reference="$OVPN_CLIENT_SELECTOR_REFERENCE"
  consumed="$OVPN_CLIENT_SELECTOR_CONSUMED"
  shift "$consumed"
  [ "$#" -eq 0 ] || ovpn_die "$usage"
  ovpn_with_data_lock client ovpn_client_export_inner "$selector_mode" "$reference"
}
