#!/usr/bin/env bash

OVPN_MIGRATION_1_TO_2_LOADED=true

ovpn_migration_1_to_2_collect_clients() {
  local index="$OVPN_DATA_DIR/pki/index.txt"
  local line status subject name

  declare -gA OVPN_MIGRATION_1_TO_2_STATES=()
  [ -r "$index" ] || ovpn_die "cannot migrate schema 1 without PKI index: $index"
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in V|R) ;; *) continue ;; esac
    subject="${line##*$'\t'}"
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ "$name" = "$OVPN_SERVER_NAME" ] && continue
    ovpn_registry_client_name_valid "$name" || ovpn_die "invalid client name in PKI index: $name"
    if [ "$status" = V ]; then
      OVPN_MIGRATION_1_TO_2_STATES["$name"]=active
    elif [ -z "${OVPN_MIGRATION_1_TO_2_STATES[$name]:-}" ]; then
      OVPN_MIGRATION_1_TO_2_STATES["$name"]=revoked
    fi
  done <"$index"
}

ovpn_migration_1_to_2_client_count() {
  ovpn_migration_1_to_2_collect_clients
  printf '%s\n' "${#OVPN_MIGRATION_1_TO_2_STATES[@]}"
}

ovpn_migration_1_to_2_load_legacy_config() {
  ovpn_config_defaults
  [ -r "$OVPN_PROJECT_ENV" ] || ovpn_die "missing schema 1 project configuration: $OVPN_PROJECT_ENV"
  ovpn_config_load_file
  [ "$OVPN_CONFIG_VERSION" = 1 ] || ovpn_die "expected schema 1 configuration"
  OVPN_CONFIG_VERSION=2
  ovpn_config_validate
}

ovpn_migration_1_to_2_write_clients() {
  local client_ip_file="$1"
  local state_file="$2"
  local name

  ovpn_migration_1_to_2_collect_clients
  umask 077
  {
    printf '%s\n' '# client,ip'
    for name in "${!OVPN_MIGRATION_1_TO_2_STATES[@]}"; do
      printf '%s,\n' "$name"
    done | LC_ALL=C sort
  } >"$client_ip_file.tmp"
  {
    printf '%s\n' '# client,state'
    for name in "${!OVPN_MIGRATION_1_TO_2_STATES[@]}"; do
      printf '%s,%s\n' "$name" "${OVPN_MIGRATION_1_TO_2_STATES[$name]}"
    done | LC_ALL=C sort
  } >"$state_file.tmp"
  mv "$client_ip_file.tmp" "$client_ip_file"
  mv "$state_file.tmp" "$state_file"
  chmod 600 "$client_ip_file" "$state_file"
}

ovpn_migration_1_to_2_apply_staged() {
  local client_ip_file state_file audit_file applied_file

  ovpn_migration_1_to_2_load_legacy_config
  client_ip_file="$(ovpn_registry_client_ip_file)"
  state_file="$(ovpn_registry_client_state_file)"
  audit_file="$(ovpn_registry_audit_file)"
  applied_file="$(ovpn_registry_applied_file)"
  mkdir -p "$(dirname "$client_ip_file")" "$(dirname "$state_file")"
  ovpn_migration_1_to_2_write_clients "$client_ip_file" "$state_file"
  : >"$audit_file"
  cp "$client_ip_file" "$applied_file.tmp"
  mv "$applied_file.tmp" "$applied_file"
  chmod 600 "$applied_file" "$audit_file"
  ovpn_config_write_loaded
  ovpn_log 'migrated schema 1 client state to the schema 2 dynamic registry'
}
