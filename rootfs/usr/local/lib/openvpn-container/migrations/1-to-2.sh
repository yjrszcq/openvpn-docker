#!/usr/bin/env bash

OVPN_MIGRATION_1_TO_2_LOADED=true

ovpn_migration_1_to_2_legacy_name_valid() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]
}

ovpn_migration_1_to_2_collect_clients() {
  local data_dir="${1:-$OVPN_DATA_DIR}"
  local index="$data_dir/pki/index.txt"
  local line status subject name

  declare -gA OVPN_MIGRATION_1_TO_2_STATES=()
  [ -r "$index" ] || ovpn_die "cannot migrate schema 1 without PKI index: $index"
  while IFS= read -r line || [ -n "$line" ]; do
    status="${line%%$'\t'*}"
    case "$status" in V|R) ;; *) continue ;; esac
    subject="${line##*$'\t'}"
    [[ "$subject" == */CN=* ]] ||
      ovpn_die 'schema 1 PKI index contains a client entry without a CN'
    name="${subject##*/CN=}"
    name="${name%%/*}"
    [ "$name" = "$OVPN_SERVER_NAME" ] && continue
    ovpn_migration_1_to_2_legacy_name_valid "$name" ||
      ovpn_die "invalid client name in schema 1 PKI index: $name"
    if [ "$status" = V ]; then
      OVPN_MIGRATION_1_TO_2_STATES["$name"]=active
    elif [ -z "${OVPN_MIGRATION_1_TO_2_STATES[$name]:-}" ]; then
      OVPN_MIGRATION_1_TO_2_STATES["$name"]=revoked
    fi
  done <"$index"
}

ovpn_migration_1_to_2_client_count() {
  ovpn_migration_1_to_2_collect_clients "${1:-$OVPN_DATA_DIR}"
  printf '%s\n' "${#OVPN_MIGRATION_1_TO_2_STATES[@]}"
}

ovpn_migration_1_to_2_config_defaults() {
  OVPN_CONFIG_VERSION=1
  OVPN_ENDPOINT=''
  OVPN_PROTO=udp
  OVPN_PORT=1194
  OVPN_NETWORK=10.8.0.0/24
  OVPN_NAT=true
  OVPN_NAT_INTERFACE=auto
  OVPN_REDIRECT_GATEWAY=false
  OVPN_CLIENT_TO_CLIENT=false
  OVPN_DNS=''
  OVPN_ROUTES=''
}

ovpn_migration_1_to_2_load_legacy_config() {
  local project_env="$1"
  local line key value
  local -A seen=()

  ovpn_migration_1_to_2_config_defaults
  [ -r "$project_env" ] ||
    ovpn_die "missing schema 1 project configuration: $project_env"
  [ "$(ovpn_migration_1_to_2_read_project_version "$project_env")" = 1 ] ||
    ovpn_die 'expected schema 1 configuration'
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|'#'*) continue ;; esac
    [[ "$line" == *=* ]] ||
      ovpn_die "invalid schema 1 config line: $line"
    key="${line%%=*}"
    value="${line#*=}"
    [ -z "${seen[$key]+present}" ] ||
      ovpn_die "duplicate schema 1 config key: $key"
    seen["$key"]=1
    case "$key" in
      OVPN_CONFIG_VERSION|OVPN_ENDPOINT|OVPN_PROTO|OVPN_PORT|OVPN_NETWORK|OVPN_NAT|OVPN_NAT_INTERFACE|OVPN_REDIRECT_GATEWAY|OVPN_CLIENT_TO_CLIENT|OVPN_DNS|OVPN_ROUTES)
        printf -v "$key" '%s' "$value"
        ;;
      *) ovpn_die "unsupported schema 1 config key: $key" ;;
    esac
  done <"$project_env"
  [ "$OVPN_CONFIG_VERSION" = 1 ] || ovpn_die 'expected schema 1 configuration'

  OVPN_TRANSPORT_FAMILY=auto
  OVPN_TOPOLOGY=subnet
  OVPN_DYNAMIC_POOL_SIZE=''
  OVPN_CONFIG_VERSION=3
  ovpn_config_validate
  OVPN_CONFIG_VERSION=2
}

ovpn_migration_1_to_2_write_config() {
  local project_env="$1"
  local schema_file="$2"

  umask 077
  {
    printf 'OVPN_CONFIG_VERSION=2\n'
    printf 'OVPN_ENDPOINT=%s\n' "$OVPN_ENDPOINT"
    printf 'OVPN_PROTO=%s\n' "$OVPN_PROTO"
    printf 'OVPN_TRANSPORT_FAMILY=auto\n'
    printf 'OVPN_PORT=%s\n' "$OVPN_PORT"
    printf 'OVPN_NETWORK=%s\n' "$OVPN_NETWORK"
    printf 'OVPN_TOPOLOGY=subnet\n'
    printf 'OVPN_DYNAMIC_POOL_SIZE=%s\n' "$OVPN_DYNAMIC_POOL_SIZE"
    printf 'OVPN_NAT=%s\n' "$OVPN_NAT"
    printf 'OVPN_NAT_INTERFACE=%s\n' "$OVPN_NAT_INTERFACE"
    printf 'OVPN_REDIRECT_GATEWAY=%s\n' "$OVPN_REDIRECT_GATEWAY"
    printf 'OVPN_CLIENT_TO_CLIENT=%s\n' "$OVPN_CLIENT_TO_CLIENT"
    printf 'OVPN_DNS=%s\n' "$OVPN_DNS"
    printf 'OVPN_ROUTES=%s\n' "$OVPN_ROUTES"
  } >"$project_env.tmp"
  printf '2\n' >"$schema_file.tmp"
  mv "$project_env.tmp" "$project_env"
  mv "$schema_file.tmp" "$schema_file"
  chmod 600 "$project_env" "$schema_file"
}

ovpn_migration_1_to_2_write_clients() {
  local data_dir="$1"
  local client_ip_file="$data_dir/data/client-ip.csv"
  local state_file="$data_dir/meta/client-state.csv"
  local name

  ovpn_migration_1_to_2_collect_clients "$data_dir"
  mkdir -p "$(dirname "$client_ip_file")" "$(dirname "$state_file")"
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

ovpn_migration_1_to_2_apply_staged() (
  local data_dir="$1"
  local client_ip_file="$data_dir/data/client-ip.csv"
  local applied_file="$data_dir/meta/client-ip.applied.csv"
  local audit_file="$data_dir/meta/audit.jsonl"
  local project_env="$data_dir/config/project.env"
  local schema_file="$data_dir/config/schema-version"

  ovpn_migration_1_to_2_load_legacy_config "$project_env"
  ovpn_migration_1_to_2_write_clients "$data_dir"
  : >"$audit_file"
  cp "$client_ip_file" "$applied_file.tmp"
  mv "$applied_file.tmp" "$applied_file"
  chmod 600 "$applied_file" "$audit_file"
  ovpn_migration_1_to_2_write_config "$project_env" "$schema_file"
  ovpn_log 'migrated schema 1 client state to the schema 2 intermediate registry'
)

ovpn_migration_1_to_2_validate_staged() {
  local data_dir="$1"
  local project_env="$data_dir/config/project.env"
  local schema_file="$data_dir/config/schema-version"

  [ "$(ovpn_migration_1_to_2_read_project_version "$project_env")" = 2 ] &&
    [ "$(ovpn_migration_1_to_2_read_schema_file "$schema_file")" = 2 ] ||
    ovpn_die 'schema 1 to 2 migration did not write schema 2 metadata'
  [ -r "$data_dir/data/client-ip.csv" ] &&
    [ -r "$data_dir/meta/client-ip.applied.csv" ] &&
    [ -r "$data_dir/meta/client-state.csv" ] &&
    [ -r "$data_dir/meta/audit.jsonl" ] ||
    ovpn_die 'schema 1 to 2 migration did not write the complete registry'
  cmp "$data_dir/data/client-ip.csv" "$data_dir/meta/client-ip.applied.csv" >/dev/null ||
    ovpn_die 'schema 2 client IP snapshot does not match its draft'
  grep -Fqx '# client,ip' "$data_dir/data/client-ip.csv" ||
    ovpn_die 'schema 2 client IP registry header is invalid'
  grep -Fqx '# client,state' "$data_dir/meta/client-state.csv" ||
    ovpn_die 'schema 2 client state registry header is invalid'
}

ovpn_migration_1_to_2_read_project_version() {
  local file="$1"
  awk -F= '
    $1 == "OVPN_CONFIG_VERSION" { value = $2; count++ }
    END {
      if (count != 1 || value !~ /^[1-9][0-9]*$/) exit 1
      print value
    }
  ' "$file"
}

ovpn_migration_1_to_2_read_schema_file() {
  local file="$1"
  local value extra

  IFS= read -r value <"$file" || return 1
  IFS= read -r extra < <(sed -n '2p' "$file") || true
  [[ "$value" =~ ^[1-9][0-9]*$ ]] && [ -z "$extra" ] || return 1
  printf '%s\n' "$value"
}
