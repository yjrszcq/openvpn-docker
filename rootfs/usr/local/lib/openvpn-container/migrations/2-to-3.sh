#!/usr/bin/env bash
# shellcheck disable=SC2034

OVPN_MIGRATION_2_TO_3_LOADED=true
declare -ga OVPN_MIGRATION_2_TO_3_NAMES=()
declare -gA OVPN_MIGRATION_2_TO_3_STATES=()
declare -gA OVPN_MIGRATION_2_TO_3_IDS=()
declare -gA OVPN_MIGRATION_2_TO_3_DRAFT_IPS=()
declare -gA OVPN_MIGRATION_2_TO_3_APPLIED_IPS=()

ovpn_migration_2_to_3_legacy_name_valid() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]
}

ovpn_migration_2_to_3_read_project_version() {
  local file="$1"

  awk -F= '
    $1 == "OVPN_CONFIG_VERSION" { value = $2; count++ }
    END {
      if (count != 1 || value !~ /^[1-9][0-9]*$/) exit 1
      print value
    }
  ' "$file"
}

ovpn_migration_2_to_3_read_schema_file() {
  local file="$1"
  local value extra

  [ -r "$file" ] || return 1
  IFS= read -r value <"$file" || return 1
  IFS= read -r extra < <(sed -n '2p' "$file") || true
  [[ "$value" =~ ^[1-9][0-9]*$ ]] && [ -z "$extra" ] || return 1
  printf '%s\n' "$value"
}

ovpn_migration_2_to_3_load_config() {
  local data_dir="$1"
  local project_env="$data_dir/config/project.env"
  local schema_file="$data_dir/config/schema-version"
  local line key value
  local -A seen=()

  if [ "$(ovpn_migration_2_to_3_read_project_version "$project_env")" != 2 ] ||
    [ "$(ovpn_migration_2_to_3_read_schema_file "$schema_file")" != 2 ]; then
    ovpn_die 'expected matching schema 2 configuration metadata'
  fi
  OVPN_CONFIG_VERSION=2
  OVPN_ENDPOINT=''
  OVPN_PROTO=udp
  OVPN_TRANSPORT_FAMILY=auto
  OVPN_PORT=1194
  OVPN_NETWORK=10.8.0.0/24
  OVPN_TOPOLOGY=subnet
  OVPN_DYNAMIC_POOL_SIZE=''
  OVPN_NAT=false
  OVPN_NAT_INTERFACE=auto
  OVPN_REDIRECT_GATEWAY=false
  OVPN_CLIENT_TO_CLIENT=true
  OVPN_DNS=''
  OVPN_ROUTES=''
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in '' | '#'*) continue ;; esac
    [[ "$line" == *=* ]] || ovpn_die "invalid schema 2 config line: $line"
    key="${line%%=*}"
    value="${line#*=}"
    [ -z "${seen[$key]+present}" ] || ovpn_die "duplicate schema 2 config key: $key"
    seen["$key"]=1
    case "$key" in
    OVPN_CONFIG_VERSION | OVPN_ENDPOINT | OVPN_PROTO | OVPN_TRANSPORT_FAMILY | OVPN_PORT | OVPN_NETWORK | OVPN_TOPOLOGY | OVPN_DYNAMIC_POOL_SIZE | OVPN_NAT | OVPN_NAT_INTERFACE | OVPN_REDIRECT_GATEWAY | OVPN_CLIENT_TO_CLIENT | OVPN_DNS | OVPN_ROUTES)
      printf -v "$key" '%s' "$value"
      ;;
    *) ovpn_die "unsupported schema 2 config key: $key" ;;
    esac
  done <"$project_env"
  [ "$OVPN_CONFIG_VERSION" = 2 ] || ovpn_die 'expected schema 2 configuration'
  OVPN_CONFIG_VERSION=3
  ovpn_config_validate
}

ovpn_migration_2_to_3_load_states() {
  local data_dir="$1"
  local file="$data_dir/meta/client-state.csv"
  local line line_number=0 name state extra header_seen=false
  local -A seen=()

  OVPN_MIGRATION_2_TO_3_NAMES=()
  OVPN_MIGRATION_2_TO_3_STATES=()
  [ -r "$file" ] || ovpn_die "missing schema 2 client state registry: $file"
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [ "$line" = '# client,state' ]; then
      if [ "$line_number" -ne 1 ] || [ "$header_seen" != false ]; then
        ovpn_die 'schema 2 client state header is invalid'
      fi
      header_seen=true
      continue
    fi
    if [ "$header_seen" != true ] || [ -z "$line" ] ||
      [[ "$line" == *[[:space:]]* ]] || [[ "$line" != *,* ]] || [[ "$line" == *,*,* ]]; then
      ovpn_die "invalid schema 2 client state row at line $line_number"
    fi
    IFS=, read -r name state extra <<<"$line"
    [ -z "${extra:-}" ] || ovpn_die "invalid schema 2 client state row at line $line_number"
    ovpn_migration_2_to_3_legacy_name_valid "$name" ||
      ovpn_die "invalid schema 2 client name: $name"
    ! ovpn_registry_uuid_valid "$name" ||
      ovpn_die "schema 2 client name is reserved by schema 3 UUID identity: $name"
    case "$state" in active | revoked | deleted) ;; *) ovpn_die "invalid schema 2 client state: $state" ;; esac
    [ -z "${seen[$name]+present}" ] || ovpn_die "duplicate schema 2 client name: $name"
    seen["$name"]=1
    OVPN_MIGRATION_2_TO_3_NAMES+=("$name")
    OVPN_MIGRATION_2_TO_3_STATES["$name"]="$state"
  done <"$file"
  [ "$header_seen" = true ] || ovpn_die 'schema 2 client state registry has no header'
}

ovpn_migration_2_to_3_load_ip_file() {
  local file="$1"
  local line line_number=0 name ip extra header_seen=false
  local -A names=()
  local -A ips=()
  local -n target_ref="$2"

  target_ref=()
  [ -r "$file" ] || ovpn_die "missing schema 2 client IP registry: $file"
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [ "$line" = '# client,ip' ]; then
      if [ "$line_number" -ne 1 ] || [ "$header_seen" != false ]; then
        ovpn_die "schema 2 client IP header is invalid: $file"
      fi
      header_seen=true
      continue
    fi
    if [ "$header_seen" != true ] || [ -z "$line" ] ||
      [[ "$line" == *[[:space:]]* ]] || [[ "$line" != *,* ]] || [[ "$line" == *,*,* ]]; then
      ovpn_die "invalid schema 2 client IP row at line $line_number: $file"
    fi
    IFS=, read -r name ip extra <<<"$line"
    [ -z "${extra:-}" ] || ovpn_die "invalid schema 2 client IP row at line $line_number: $file"
    [ -n "${OVPN_MIGRATION_2_TO_3_STATES[$name]:-}" ] ||
      ovpn_die "schema 2 client IP registry contains unknown client: $name"
    [ "${OVPN_MIGRATION_2_TO_3_STATES[$name]}" != deleted ] ||
      ovpn_die "schema 2 deleted client retains an IP row: $name"
    [ -z "${names[$name]+present}" ] || ovpn_die "duplicate schema 2 client IP row: $name"
    names["$name"]=1
    if [ -n "$ip" ]; then
      ovpn_ipam_ip_in_static_range "$ip" ||
        ovpn_die "schema 2 static IP is outside the configured static range: $ip"
      [ -z "${ips[$ip]+present}" ] || ovpn_die "duplicate schema 2 static IP: $ip"
      ips["$ip"]=1
    fi
    target_ref["$name"]="$ip"
  done <"$file"
  [ "$header_seen" = true ] || ovpn_die "schema 2 client IP registry has no header: $file"
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    [ "${OVPN_MIGRATION_2_TO_3_STATES[$name]}" = deleted ] && continue
    [ -n "${names[$name]+present}" ] ||
      ovpn_die "schema 2 client IP registry is missing client: $name"
  done
}

ovpn_migration_2_to_3_load_pki() {
  local data_dir="$1"
  local index="$data_dir/pki/index.txt"
  local line status subject name state
  local -A pki_states=()

  [ -r "$index" ] || ovpn_die "missing schema 2 PKI index: $index"
  if [ ! -r "$data_dir/pki/ca.crt" ] || [ ! -r "$data_dir/pki/private/ca.key" ]; then
    ovpn_die 'schema 2 CA certificate or private key is missing'
  fi
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in V | R) ;; *) continue ;; esac
    subject="${line##*$'\t'}"
    [[ "$subject" == */CN=* ]] || ovpn_die 'schema 2 PKI entry has no CN'
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ "$name" = "$OVPN_SERVER_NAME" ] && continue
    [ -n "${OVPN_MIGRATION_2_TO_3_STATES[$name]:-}" ] ||
      ovpn_die "schema 2 PKI contains a client missing from the registry: $name"
    if [ "$status" = V ]; then
      pki_states["$name"]=active
    elif [ -z "${pki_states[$name]:-}" ]; then
      pki_states["$name"]=revoked
    fi
  done <"$index"
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
    case "$state" in
    active | revoked)
      [ "${pki_states[$name]:-}" = "$state" ] ||
        ovpn_die "schema 2 registry and PKI disagree for client: $name"
      ;;
    deleted)
      [ "${pki_states[$name]:-}" != active ] ||
        ovpn_die "schema 2 deleted client still has an active certificate: $name"
      ;;
    esac
  done
}

ovpn_migration_2_to_3_load() {
  local data_dir="$1"

  ovpn_migration_2_to_3_load_config "$data_dir"
  ovpn_migration_2_to_3_load_states "$data_dir"
  ovpn_migration_2_to_3_load_ip_file "$data_dir/data/client-ip.csv" OVPN_MIGRATION_2_TO_3_DRAFT_IPS
  ovpn_migration_2_to_3_load_ip_file "$data_dir/meta/client-ip.applied.csv" OVPN_MIGRATION_2_TO_3_APPLIED_IPS
  ovpn_migration_2_to_3_load_pki "$data_dir"
}

ovpn_migration_2_to_3_client_count() {
  ovpn_migration_2_to_3_load "$OVPN_DATA_DIR"
  printf '%s\n' "${#OVPN_MIGRATION_2_TO_3_NAMES[@]}"
}

ovpn_migration_2_to_3_assign_ids() {
  local name id

  OVPN_MIGRATION_2_TO_3_IDS=()
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    id="$(ovpn_registry_uuid_generate)" || ovpn_die "failed to generate UUID for schema 2 client: $name"
    OVPN_MIGRATION_2_TO_3_IDS["$name"]="$id"
  done
}

ovpn_migration_2_to_3_convert_audit() {
  local data_dir="$1"
  local audit_file="$data_dir/meta/audit.jsonl"
  local converted="$audit_file.tmp"
  local line line_number=0 timestamp event outcome operation
  local timestamp_pattern='([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)'
  local global_regex lifecycle_regex

  [ -r "$audit_file" ] || ovpn_die "missing schema 2 audit log: $audit_file"
  global_regex="^\\{\"timestamp\":\"${timestamp_pattern}\",\"event\":\"(client_ip_apply|network_migration)\",\"outcome\":\"(applied|rejected)\"\\}$"
  lifecycle_regex="^\\{\"timestamp\":\"${timestamp_pattern}\",\"operation\":\"(revoke|reissue|delete|release_ip)\",\"result\":\"(applied|rejected|failed)\"\\}$"
  : >"$converted"
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [[ "$line" =~ $global_regex ]]; then
      timestamp="${BASH_REMATCH[1]}"
      event="${BASH_REMATCH[2]}"
      outcome="${BASH_REMATCH[3]}"
      printf '{"timestamp":"%s","event":"%s","outcome":"%s","client_id":null,"client_name":null,"legacy":true,"source_schema":2}\n' \
        "$timestamp" "$event" "$outcome" >>"$converted"
      continue
    fi
    if [[ "$line" =~ $lifecycle_regex ]]; then
      timestamp="${BASH_REMATCH[1]}"
      operation="${BASH_REMATCH[2]}"
      outcome="${BASH_REMATCH[3]}"
      printf '{"timestamp":"%s","event":"client_lifecycle","operation":"%s","outcome":"%s","client_id":null,"client_name":null,"legacy":true,"source_schema":2}\n' \
        "$timestamp" "$operation" "$outcome" >>"$converted"
      continue
    fi
    rm -f "$converted"
    ovpn_die "unsupported schema 2 audit record at line $line_number"
  done <"$audit_file"
  mv "$converted" "$audit_file"
  chmod 600 "$audit_file"
}

ovpn_migration_2_to_3_write_registries() {
  local data_dir="$1"
  local name id state

  umask 077
  {
    printf '# id,name,state\n'
    for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
      printf '%s,%s,%s\n' "${OVPN_MIGRATION_2_TO_3_IDS[$name]}" "$name" \
        "${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
    done | LC_ALL=C sort -t, -k2,2 -k1,1
  } >"$data_dir/meta/client-state.csv.tmp"
  {
    printf '# id,name,ip\n'
    for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
      state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
      [ "$state" = deleted ] && continue
      id="${OVPN_MIGRATION_2_TO_3_IDS[$name]}"
      printf '%s,%s,%s\n' "$id" "$name" "${OVPN_MIGRATION_2_TO_3_DRAFT_IPS[$name]}"
    done | LC_ALL=C sort -t, -k2,2 -k1,1
  } >"$data_dir/data/client-ip.csv.tmp"
  {
    printf '# id,name,ip\n'
    for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
      state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
      [ "$state" = deleted ] && continue
      id="${OVPN_MIGRATION_2_TO_3_IDS[$name]}"
      printf '%s,%s,%s\n' "$id" "$name" "${OVPN_MIGRATION_2_TO_3_APPLIED_IPS[$name]}"
    done | LC_ALL=C sort -t, -k2,2 -k1,1
  } >"$data_dir/meta/client-ip.applied.csv.tmp"
  mv "$data_dir/meta/client-state.csv.tmp" "$data_dir/meta/client-state.csv"
  mv "$data_dir/data/client-ip.csv.tmp" "$data_dir/data/client-ip.csv"
  mv "$data_dir/meta/client-ip.applied.csv.tmp" "$data_dir/meta/client-ip.applied.csv"
  chmod 600 "$data_dir/meta/client-state.csv" "$data_dir/data/client-ip.csv" \
    "$data_dir/meta/client-ip.applied.csv"
}

ovpn_migration_2_to_3_write_config() {
  local data_dir="$1"

  OVPN_CONFIG_VERSION=3
  OVPN_CONFIG_DIR="$data_dir/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  ovpn_config_write_loaded
}

ovpn_migration_2_to_3_reissue_pki() {
  local data_dir="$1"
  local name id state

  OVPN_DATA_DIR="$data_dir"
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
    [ "$state" = active ] || continue
    ovpn_run_easyrsa revoke "$name" ||
      ovpn_die "failed to revoke schema 2 certificate for client: $name"
  done
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
    [ "$state" != deleted ] || continue
    id="${OVPN_MIGRATION_2_TO_3_IDS[$name]}"
    ovpn_pki_issue_client "$id"
  done
}

ovpn_migration_2_to_3_finalize_pki() {
  local data_dir="$1"
  local name id state

  OVPN_DATA_DIR="$data_dir"
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
    [ "$state" = revoked ] || continue
    id="${OVPN_MIGRATION_2_TO_3_IDS[$name]}"
    ovpn_run_easyrsa revoke "$id" ||
      ovpn_die "failed to revoke replacement certificate for client: $name"
  done
  ovpn_pki_generate_crl
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    rm -f "$data_dir/pki/private/$name.key" "$data_dir/pki/issued/$name.crt" \
      "$data_dir/pki/reqs/$name.req"
  done
}

ovpn_migration_2_to_3_rebuild_derived() {
  local data_dir="$1"
  local final_data_dir="$2"
  local name id state ip old_lease content

  OVPN_DATA_DIR="$data_dir"
  OVPN_CONFIG_DIR="$data_dir/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  OVPN_RENDER_DATA_DIR="$final_data_dir"
  OVPN_LEASE_DIR="$data_dir/data/leases"
  rm -rf "$data_dir/ccd" "$data_dir/clients/active" "$data_dir/clients/revoked"
  mkdir -p "$data_dir/ccd" "$data_dir/clients/active" "$data_dir/clients/revoked"
  chmod 700 "$data_dir/ccd"
  content="$(ovpn_render_server_content)" ||
    ovpn_die 'failed to render the migrated server configuration'
  ovpn_write_or_print "$data_dir/server/server.conf" "$content"
  for name in "${OVPN_MIGRATION_2_TO_3_NAMES[@]}"; do
    id="${OVPN_MIGRATION_2_TO_3_IDS[$name]}"
    state="${OVPN_MIGRATION_2_TO_3_STATES[$name]}"
    [ "$state" != deleted ] || {
      rm -f "$data_dir/data/leases/$name"
      continue
    }
    content="$(ovpn_render_client_content "$name")" ||
      ovpn_die "failed to render migrated profile for client: $name"
    ovpn_write_or_print "$data_dir/clients/$state/$name.ovpn" "$content"
    ip="${OVPN_MIGRATION_2_TO_3_APPLIED_IPS[$name]}"
    if [ -n "$ip" ]; then
      printf 'ifconfig-push %s %s\n' "$ip" "$OVPN_IPAM_NETMASK" >"$data_dir/ccd/$id"
      chmod 600 "$data_dir/ccd/$id"
    fi
    old_lease="$data_dir/data/leases/$name"
    if [ -f "$old_lease" ]; then
      mv "$old_lease" "$data_dir/data/leases/$id"
      chmod 600 "$data_dir/data/leases/$id"
    fi
  done
}

ovpn_migration_2_to_3_apply_staged() (
  local data_dir="$1"
  local final_data_dir="${2:-$OVPN_DATA_DIR}"

  ovpn_migration_2_to_3_load "$data_dir"
  ovpn_migration_2_to_3_assign_ids
  ovpn_migration_2_to_3_convert_audit "$data_dir"
  ovpn_migration_2_to_3_write_registries "$data_dir"
  ovpn_migration_2_to_3_write_config "$data_dir"
  ovpn_migration_2_to_3_reissue_pki "$data_dir"
  ovpn_migration_2_to_3_rebuild_derived "$data_dir" "$final_data_dir"
  ovpn_migration_2_to_3_finalize_pki "$data_dir"
  ovpn_log 'migrated schema 2 clients to UUID certificate identities'
)

ovpn_migration_2_to_3_validate_staged() (
  local data_dir="$1"
  local index line status subject name id state profile
  local -A pki_states=()

  OVPN_DATA_DIR="$data_dir"
  OVPN_CONFIG_DIR="$data_dir/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  if [ "$(ovpn_schema_read_project_version)" != 3 ] ||
    [ "$(ovpn_schema_read_version_file)" != 3 ]; then
    ovpn_die 'schema 2 to 3 migration did not write schema 3 metadata'
  fi
  ovpn_config_load
  ovpn_registry_load_identities || ovpn_die 'migrated schema 3 identity registry is invalid'
  ovpn_client_ip_validate_file "$data_dir/data/client-ip.csv" ||
    ovpn_die 'migrated schema 3 client IP draft is invalid'
  ovpn_client_ip_validate_file "$data_dir/meta/client-ip.applied.csv" ||
    ovpn_die 'migrated schema 3 applied client IP registry is invalid'
  ovpn_state_ipam_audit_is_valid "$data_dir/meta/audit.jsonl" ||
    ovpn_die 'migrated schema 3 audit log is invalid'
  index="$data_dir/pki/index.txt"
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in V | R) ;; *) continue ;; esac
    subject="${line##*$'\t'}"
    id="${subject##*/CN=}"
    id="${id%%/*}"
    [ "$id" = "$OVPN_SERVER_NAME" ] && continue
    if ! ovpn_registry_uuid_valid "$id"; then
      [ "$status" = R ] || ovpn_die "migrated PKI retains active legacy client CN: $id"
      continue
    fi
    if [ "$status" = V ]; then
      pki_states["$id"]=active
    elif [ -z "${pki_states[$id]:-}" ]; then
      pki_states["$id"]=revoked
    fi
  done <"$index"
  for id in "${OVPN_REGISTRY_CLIENT_IDS[@]}"; do
    name="${OVPN_REGISTRY_NAME_BY_ID[$id]}"
    state="${OVPN_REGISTRY_STATE_BY_ID[$id]}"
    if [ "$state" = deleted ]; then
      [ -z "${pki_states[$id]:-}" ] ||
        ovpn_die "migrated deleted client has a replacement certificate: $name"
      continue
    fi
    [ "${pki_states[$id]:-}" = "$state" ] ||
      ovpn_die "migrated client identity and PKI disagree: $name"
    profile="$data_dir/clients/$state/$name.ovpn"
    [ -r "$profile" ] || ovpn_die "migrated client profile is missing: $name"
    grep -Fqx "# ovpn-client-id: $id" "$profile" ||
      ovpn_die "migrated client profile has no UUID recovery comment: $name"
  done
  if [ ! -r "$data_dir/pki/crl.pem" ] || [ ! -r "$data_dir/server/server.conf" ]; then
    ovpn_die 'migrated CRL or server configuration is missing'
  fi
)
