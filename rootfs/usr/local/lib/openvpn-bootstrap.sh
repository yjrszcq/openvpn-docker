#!/usr/bin/env bash

OVPN_EMBEDDED_MANAGEMENT_ROOT="${OVPN_EMBEDDED_MANAGEMENT_ROOT:-/usr/local/share/openvpn-container/embedded-management}"
OVPN_RUNTIME_MANAGEMENT_ROOT="${OVPN_RUNTIME_MANAGEMENT_ROOT:-/usr/local/lib/openvpn-management-runtime}"

ovpn_bootstrap_read_metadata() {
  local file="$OVPN_EMBEDDED_MANAGEMENT_ROOT/management.env"
  local line key value

  OVPN_BOOTSTRAP_MANAGEMENT_VERSION=''
  OVPN_BOOTSTRAP_PLATFORM_API=''
  OVPN_BOOTSTRAP_DATA_SCHEMA=''
  [ -r "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|'#'*) continue ;; esac
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

ovpn_bootstrap_exec() {
  local selected_lib

  if [ -n "${OVPN_LIB_DIR:-}" ] && [ -x "$OVPN_LIB_DIR/cli.sh" ]; then
    exec "$OVPN_LIB_DIR/cli.sh" "$@"
  fi
  ovpn_bootstrap_activate_embedded || return $?
  selected_lib="$OVPN_BOOTSTRAP_SELECTED_ROOT/lib"
  export OVPN_LIB_DIR="$selected_lib"
  export OVPN_TEMPLATE_ROOT="$OVPN_BOOTSTRAP_SELECTED_ROOT/templates"
  export OVPN_COMPATIBILITY_DIR="$OVPN_BOOTSTRAP_SELECTED_ROOT/compatibility"
  exec "$selected_lib/cli.sh" "$@"
}
