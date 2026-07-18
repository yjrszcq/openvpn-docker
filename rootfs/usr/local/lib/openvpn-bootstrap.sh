#!/usr/bin/env bash

OVPN_EMBEDDED_MANAGEMENT_ROOT="${OVPN_EMBEDDED_MANAGEMENT_ROOT:-/usr/local/share/openvpn-container/embedded-management}"
OVPN_RUNTIME_MANAGEMENT_ROOT="${OVPN_RUNTIME_MANAGEMENT_ROOT:-/usr/local/lib/openvpn-management-runtime}"
OVPN_MANAGEMENT_STORE="${OVPN_MANAGEMENT_STORE:-${OVPN_DATA_DIR:-/etc/openvpn}/repair/.scripts}"
OVPN_MANAGEMENT_KEYRING="${OVPN_MANAGEMENT_KEYRING:-/usr/local/share/openvpn-container/trusted-management-keys}"
OVPN_MANAGEMENT_VERIFIER="${OVPN_MANAGEMENT_VERIFIER:-/usr/local/lib/openvpn-verify-management-release.sh}"

ovpn_bootstrap_read_metadata() {
  local file="$OVPN_EMBEDDED_MANAGEMENT_ROOT/management.env"
  local line key value

  OVPN_BOOTSTRAP_MANAGEMENT_VERSION=''
  OVPN_BOOTSTRAP_PLATFORM_API=''
  OVPN_BOOTSTRAP_DATA_SCHEMA=''
  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in '' | '#'*) continue ;; esac
    [[ "$line" == *=* ]] || return 1
    key="${line%%=*}"
    value="${line#*=}"
    case "$key" in
    MANAGEMENT_VERSION) OVPN_BOOTSTRAP_MANAGEMENT_VERSION="$value" ;;
    PLATFORM_API) OVPN_BOOTSTRAP_PLATFORM_API="$value" ;;
    DATA_SCHEMA) OVPN_BOOTSTRAP_DATA_SCHEMA="$value" ;;
    *) return 1 ;;
    esac
  done <"$file"
  [[ "$OVPN_BOOTSTRAP_MANAGEMENT_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || return 1
  [[ "$OVPN_BOOTSTRAP_PLATFORM_API" =~ ^[1-9][0-9]*$ ]] || return 1
  [[ "$OVPN_BOOTSTRAP_DATA_SCHEMA" =~ ^[1-9][0-9]*$ ]] || return 1
}

ovpn_bootstrap_activate_embedded() {
  local release_id target stage current_tmp

  ovpn_bootstrap_read_metadata || {
    printf 'ovpn bootstrap: invalid embedded management metadata\n' >&2
    return 78
  }
  [ -x "$OVPN_EMBEDDED_MANAGEMENT_ROOT/lib/cli.sh" ] || {
    printf 'ovpn bootstrap: embedded management CLI is missing\n' >&2
    return 78
  }
  release_id="embedded-$OVPN_BOOTSTRAP_MANAGEMENT_VERSION"
  target="$OVPN_RUNTIME_MANAGEMENT_ROOT/releases/$release_id"
  mkdir -p "$OVPN_RUNTIME_MANAGEMENT_ROOT/releases"
  exec {bootstrap_lock_fd}>"$OVPN_RUNTIME_MANAGEMENT_ROOT/.bootstrap.lock"
  flock -x "$bootstrap_lock_fd"
  if [ ! -f "$target/.ready" ]; then
    stage="$(mktemp -d "$OVPN_RUNTIME_MANAGEMENT_ROOT/releases/.embedded.XXXXXX")" || return 74
    cp -a "$OVPN_EMBEDDED_MANAGEMENT_ROOT/." "$stage/" || {
      rm -rf "$stage"
      return 74
    }
    : >"$stage/.ready"
    chmod 600 "$stage/.ready"
    if ! mv "$stage" "$target" 2>/dev/null; then
      rm -rf "$stage"
      [ -f "$target/.ready" ] || return 74
    fi
  fi
  current_tmp="$OVPN_RUNTIME_MANAGEMENT_ROOT/.current.$$"
  ln -s "$target" "$current_tmp"
  mv -Tf "$current_tmp" "$OVPN_RUNTIME_MANAGEMENT_ROOT/current"
  flock -u "$bootstrap_lock_fd"

  OVPN_BOOTSTRAP_SELECTED_ROOT="$target"
  export OVPN_ACTIVE_MANAGEMENT_VERSION="$OVPN_BOOTSTRAP_MANAGEMENT_VERSION"
  export OVPN_MANAGEMENT_SOURCE=embedded
}

ovpn_bootstrap_read_pointer() {
  local file="$1" value extra

  [ -r "$file" ] || return 1
  IFS= read -r value <"$file" || return 1
  IFS= read -r extra < <(sed -n '2p' "$file") || true
  [ -z "$extra" ] || return 1
  if [ "$value" = embedded ] || [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf '%s\n' "$value"
    return 0
  fi
  return 1
}

ovpn_bootstrap_write_pointer() {
  local name="$1" value="$2" temporary
  temporary="$OVPN_MANAGEMENT_STORE/.$name.$$"
  printf '%s\n' "$value" >"$temporary" || return 1
  chmod 600 "$temporary" || return 1
  mv -f "$temporary" "$OVPN_MANAGEMENT_STORE/$name"
}

ovpn_bootstrap_recover_selector_transaction() {
  local file="$OVPN_MANAGEMENT_STORE/transactions/activation.env"
  local expected key value state='' old_active='' old_previous='' new_active='' new_previous=''

  [ -e "$file" ] || return 0
  [ -r "$file" ] || return 1
  expected='STATE OLD_ACTIVE OLD_PREVIOUS NEW_ACTIVE NEW_PREVIOUS'
  [ "$(awk -F= 'NF == 2 { print $1 }' "$file" | paste -sd' ' -)" = "$expected" ] || return 1
  [ "$(wc -l <"$file" | tr -d ' ')" -eq 5 ] || return 1
  while IFS='=' read -r key value; do
    if [ "$key" = STATE ]; then
      case "$value" in prepared | committed) state="$value" ;; *) return 1 ;; esac
    else
      if [ "$value" != embedded ] && ! [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        return 1
      fi
      case "$key" in
      OLD_ACTIVE) old_active="$value" ;;
      OLD_PREVIOUS) old_previous="$value" ;;
      NEW_ACTIVE) new_active="$value" ;;
      NEW_PREVIOUS) new_previous="$value" ;;
      *) return 1 ;;
      esac
    fi
  done <"$file"
  if [ "$state" = committed ]; then
    ovpn_bootstrap_write_pointer previous "$new_previous" || return 1
    ovpn_bootstrap_write_pointer active "$new_active" || return 1
  else
    ovpn_bootstrap_write_pointer previous "$old_previous" || return 1
    ovpn_bootstrap_write_pointer active "$old_active" || return 1
  fi
  rm -f "$file"
}

ovpn_bootstrap_bundle_is_safe() {
  local asset="$1" entry type

  while IFS= read -r entry; do
    entry="${entry#./}"
    entry="${entry%/}"
    [ -n "$entry" ] || continue
    case "/$entry/" in
    *'/../'* | *'//'*) return 1 ;;
    esac
    [[ "$entry" != /* ]] || return 1
  done < <(tar -tzf "$asset") || return 1
  while IFS= read -r type; do
    case "$type" in - | d) ;; *) return 1 ;; esac
  done < <(tar -tvzf "$asset" | cut -c1) || return 1
}

ovpn_bootstrap_observed_data_schema() {
  local schema_file="${OVPN_DATA_DIR:-/etc/openvpn}/config/schema-version"
  local project_file="${OVPN_DATA_DIR:-/etc/openvpn}/config/project.env" value extra
  if [ -r "$schema_file" ]; then
    IFS= read -r value <"$schema_file" || return 1
    IFS= read -r extra < <(sed -n '2p' "$schema_file") || true
    [[ "$value" =~ ^[1-9][0-9]*$ ]] && [ -z "$extra" ] || return 1
    printf '%s\n' "$value"
    return 0
  fi
  if [ -r "$project_file" ]; then
    value="$(awk -F= '$1 == "OVPN_CONFIG_VERSION" { print $2 }' "$project_file")"
    [ "$(awk -F= '$1 == "OVPN_CONFIG_VERSION" { count++ } END { print count + 0 }' "$project_file")" -eq 1 ] || return 1
    [[ "$value" =~ ^[1-9][0-9]*$ ]] || return 1
    printf '%s\n' "$value"
    return 0
  fi
  ovpn_bootstrap_read_metadata || return 1
  printf '%s\n' "$OVPN_BOOTSTRAP_DATA_SCHEMA"
}

ovpn_bootstrap_activate_online() {
  local version="$1" mode="${2:-activate}" release_dir manifest asset_sha platform_min platform_max
  local target stage current_tmp target_schema observed_schema

  release_dir="$OVPN_MANAGEMENT_STORE/releases/$version"
  [ -x "$OVPN_MANAGEMENT_VERIFIER" ] || return 1
  "$OVPN_MANAGEMENT_VERIFIER" --release-dir "$release_dir" \
    --keyring "$OVPN_MANAGEMENT_KEYRING" >/dev/null 2>&1 || return 1
  manifest="$release_dir/management-release.env"
  asset_sha="$(awk -F= '$1 == "ASSET_SHA256" { print $2 }' "$manifest")"
  platform_min="$(awk -F= '$1 == "PLATFORM_API_MIN" { print $2 }' "$manifest")"
  platform_max="$(awk -F= '$1 == "PLATFORM_API_MAX" { print $2 }' "$manifest")"
  target_schema="$(awk -F= '$1 == "DATA_SCHEMA" { print $2 }' "$manifest")"
  ovpn_bootstrap_read_metadata || return 1
  [ "$OVPN_BOOTSTRAP_PLATFORM_API" -ge "$platform_min" ] &&
    [ "$OVPN_BOOTSTRAP_PLATFORM_API" -le "$platform_max" ] || return 1
  observed_schema="$(ovpn_bootstrap_observed_data_schema)" || return 1
  [ "$target_schema" = "$observed_schema" ] || return 1
  asset="$release_dir/management-bundle.tar.gz"
  ovpn_bootstrap_bundle_is_safe "$asset" || return 1

  target="$OVPN_RUNTIME_MANAGEMENT_ROOT/releases/online-$version-${asset_sha:0:12}"
  mkdir -p "$OVPN_RUNTIME_MANAGEMENT_ROOT/releases"
  exec {bootstrap_lock_fd}>"$OVPN_RUNTIME_MANAGEMENT_ROOT/.bootstrap.lock"
  flock -x "$bootstrap_lock_fd"
  if [ ! -f "$target/.ready" ]; then
    stage="$(mktemp -d "$OVPN_RUNTIME_MANAGEMENT_ROOT/releases/.online.XXXXXX")" || return 74
    if ! tar --no-same-owner --no-same-permissions -xzf "$asset" -C "$stage" ||
      [ ! -x "$stage/lib/cli.sh" ]; then
      rm -rf "$stage"
      return 74
    fi
    if ! printf '%s\n' "$asset_sha" >"$stage/.ready" || ! chmod 600 "$stage/.ready"; then
      rm -rf "$stage"
      return 74
    fi
    if ! mv "$stage" "$target" 2>/dev/null; then
      rm -rf "$stage"
      [ -f "$target/.ready" ] || return 74
    fi
  fi
  [ "$(cat "$target/.ready")" = "$asset_sha" ] || return 1
  OVPN_BOOTSTRAP_SELECTED_ROOT="$target"
  if [ "$mode" = hydrate-only ]; then
    flock -u "$bootstrap_lock_fd"
    return 0
  fi
  [ "$mode" = activate ] || return 1
  current_tmp="$OVPN_RUNTIME_MANAGEMENT_ROOT/.current.$$"
  ln -s "$target" "$current_tmp"
  mv -Tf "$current_tmp" "$OVPN_RUNTIME_MANAGEMENT_ROOT/current"
  flock -u "$bootstrap_lock_fd"

  export OVPN_ACTIVE_MANAGEMENT_VERSION="$version"
  export OVPN_MANAGEMENT_SOURCE=online
}

ovpn_bootstrap_select_management() {
  local active='' recovery_status=0

  if [ -e "$OVPN_MANAGEMENT_STORE/transactions/activation.env" ]; then
    exec {management_recovery_fd}>"$OVPN_MANAGEMENT_STORE/.management.lock" || recovery_status=1
    if [ "$recovery_status" -eq 0 ]; then
      flock -x "$management_recovery_fd" || recovery_status=1
    fi
    if [ "$recovery_status" -eq 0 ]; then
      ovpn_bootstrap_recover_selector_transaction || recovery_status=1
      flock -u "$management_recovery_fd" || true
    fi
  fi
  if [ "$recovery_status" -ne 0 ]; then
    printf 'ovpn bootstrap: invalid management activation transaction; using embedded fallback\n' >&2
    ovpn_bootstrap_activate_embedded
    return $?
  fi
  active="$(ovpn_bootstrap_read_pointer "$OVPN_MANAGEMENT_STORE/active")" || true
  if [ -n "$active" ] && [ "$active" != embedded ]; then
    if ovpn_bootstrap_activate_online "$active"; then
      return 0
    fi
    printf 'ovpn bootstrap: active management release %s is invalid; using embedded fallback\n' "$active" >&2
  fi
  ovpn_bootstrap_activate_embedded
}

ovpn_bootstrap_exec() {
  local selected_lib

  if [ -n "${OVPN_LIB_DIR:-}" ] && [ -x "$OVPN_LIB_DIR/cli.sh" ]; then
    exec "$OVPN_LIB_DIR/cli.sh" "$@"
  fi
  ovpn_bootstrap_select_management || return $?
  selected_lib="$OVPN_BOOTSTRAP_SELECTED_ROOT/lib"
  export OVPN_LIB_DIR="$selected_lib"
  export OVPN_TEMPLATE_ROOT="$OVPN_BOOTSTRAP_SELECTED_ROOT/templates"
  export OVPN_COMPATIBILITY_DIR="$OVPN_BOOTSTRAP_SELECTED_ROOT/compatibility"
  exec "$selected_lib/cli.sh" "$@"
}
