#!/usr/bin/env bash

declare -a OVPN_REGISTRY_CLIENT_IDS=()
declare -a OVPN_REGISTRY_CLIENT_NAMES=()
declare -a OVPN_REGISTRY_CLIENT_STATES=()
declare -A OVPN_REGISTRY_NAME_BY_ID=()
declare -A OVPN_REGISTRY_STATE_BY_ID=()
declare -A OVPN_REGISTRY_CURRENT_ID_BY_NAME=()

ovpn_registry_uuid_valid() {
  [[ "$1" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]
}

ovpn_registry_uuid_generate() {
  local value hex variant openssl_bin

  if [ -r /proc/sys/kernel/random/uuid ]; then
    value="$(cat /proc/sys/kernel/random/uuid)"
    ovpn_registry_uuid_valid "$value" || return 1
    printf '%s\n' "$value"
    return 0
  fi
  openssl_bin="$(ovpn_openssl_bin)" || return 1
  hex="$("$openssl_bin" rand -hex 16)" || return 1
  [[ "$hex" =~ ^[0-9a-f]{32}$ ]] || return 1
  variant=$((16#${hex:16:1}))
  printf '%s-%s-4%s-%x%s-%s\n' \
    "${hex:0:8}" "${hex:8:4}" "${hex:13:3}" "$(((variant & 3) | 8))" \
    "${hex:17:3}" "${hex:20:12}"
}

ovpn_registry_load_identities() {
  local file="${1:-$(ovpn_registry_client_state_file)}"
  local line line_number=0 id name state extra header_seen=false
  local -A ids=()
  local -A current_names=()

  OVPN_REGISTRY_CLIENT_IDS=()
  OVPN_REGISTRY_CLIENT_NAMES=()
  OVPN_REGISTRY_CLIENT_STATES=()
  OVPN_REGISTRY_NAME_BY_ID=()
  OVPN_REGISTRY_STATE_BY_ID=()
  OVPN_REGISTRY_CURRENT_ID_BY_NAME=()
  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [ "$line" = '# id,name,state' ]; then
      [ "$header_seen" = false ] && [ "$line_number" -eq 1 ] || return 1
      header_seen=true
      continue
    fi
    [ "$header_seen" = true ] || return 1
    [ -n "$line" ] && [[ "$line" != *[[:space:]]* ]] || return 1
    IFS=, read -r id name state extra <<<"$line"
    [ -z "${extra:-}" ] && [[ "$line" == *,*,* ]] && [[ "$line" != *,*,*,* ]] || return 1
    ovpn_registry_uuid_valid "$id" || return 1
    ovpn_registry_client_name_valid "$name" || return 1
    case "$state" in active | revoked | deleted) ;; *) return 1 ;; esac
    [ -z "${ids[$id]+present}" ] || return 1
    ids["$id"]=1
    if [ "$state" != deleted ]; then
      [ -z "${current_names[$name]+present}" ] || return 1
      current_names["$name"]="$id"
      OVPN_REGISTRY_CURRENT_ID_BY_NAME["$name"]="$id"
    fi
    OVPN_REGISTRY_CLIENT_IDS+=("$id")
    OVPN_REGISTRY_CLIENT_NAMES+=("$name")
    OVPN_REGISTRY_CLIENT_STATES+=("$state")
    OVPN_REGISTRY_NAME_BY_ID["$id"]="$name"
    OVPN_REGISTRY_STATE_BY_ID["$id"]="$state"
  done <"$file"
  [ "$header_seen" = true ]
}

ovpn_registry_current_id_by_name() {
  local name="$1"
  ovpn_registry_load_identities || return 1
  [ -n "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[$name]:-}" ] || return 1
  printf '%s\n' "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[$name]}"
}

ovpn_registry_dir() {
  printf '%s/data\n' "$OVPN_DATA_DIR"
}

ovpn_registry_client_ip_file() {
  printf '%s/client-ip.csv\n' "$(ovpn_registry_dir)"
}

ovpn_registry_applied_file() {
  printf '%s/meta/client-ip.applied.csv\n' "$OVPN_DATA_DIR"
}

ovpn_registry_client_state_file() {
  printf '%s/meta/client-state.csv\n' "$OVPN_DATA_DIR"
}

ovpn_registry_audit_file() {
  printf '%s/meta/audit.jsonl\n' "$OVPN_DATA_DIR"
}

ovpn_registry_write_empty() {
  local client_ip_file="$1"
  local state_file="$2"
  local audit_file="$3"

  mkdir -p "$(dirname "$client_ip_file")" "$(dirname "$state_file")"
  umask 077
  printf '%s\n' '# id,name,ip' >"$client_ip_file.tmp"
  mv "$client_ip_file.tmp" "$client_ip_file"
  printf '%s\n' '# id,name,state' >"$state_file.tmp"
  mv "$state_file.tmp" "$state_file"
  : >"$audit_file"
  chmod 600 "$client_ip_file" "$state_file" "$audit_file"
}

ovpn_registry_initialize_empty() {
  local client_ip_file state_file audit_file applied_file

  client_ip_file="$(ovpn_registry_client_ip_file)"
  state_file="$(ovpn_registry_client_state_file)"
  audit_file="$(ovpn_registry_audit_file)"
  applied_file="$(ovpn_registry_applied_file)"
  ovpn_registry_write_empty "$client_ip_file" "$state_file" "$audit_file"
  cp "$client_ip_file" "$applied_file.tmp"
  mv "$applied_file.tmp" "$applied_file"
  chmod 600 "$applied_file"
}

ovpn_registry_files_ready() {
  [ -r "$(ovpn_registry_client_ip_file)" ] && \
    [ -r "$(ovpn_registry_applied_file)" ] && \
    [ -r "$(ovpn_registry_client_state_file)" ] && \
    [ -r "$(ovpn_registry_audit_file)" ]
}

ovpn_registry_client_name_valid() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] && ! ovpn_registry_uuid_valid "$1"
}

ovpn_registry_client_is_deleted() {
  local wanted="$1"
  local index found=false

  ovpn_registry_load_identities || return 1
  [ -z "${OVPN_REGISTRY_CURRENT_ID_BY_NAME[$wanted]:-}" ] || return 1
  for ((index = 0; index < ${#OVPN_REGISTRY_CLIENT_IDS[@]}; index++)); do
    [ "${OVPN_REGISTRY_CLIENT_NAMES[index]}" = "$wanted" ] || continue
    [ "${OVPN_REGISTRY_CLIENT_STATES[index]}" = deleted ] && found=true
  done
  [ "$found" = true ]
}
