#!/usr/bin/env bash

ovpn_with_lock() {
  local name="$1"
  shift
  local lock_dir="$OVPN_RUNTIME_DIR"
  local lock_file="$lock_dir/$name.lock"

  mkdir -p "$lock_dir"
  if command -v flock >/dev/null 2>&1; then
    (
      flock -x 9
      "$@"
    ) 9>"$lock_file"
    return $?
  fi

  local mkdir_lock="$lock_file.d"
  until mkdir "$mkdir_lock" 2>/dev/null; do
    sleep 1
  done
  trap 'rmdir "$mkdir_lock" 2>/dev/null || true' RETURN
  "$@"
}

ovpn_with_data_lock() {
  local name="$1"
  shift
  local lock_file

  case "$name" in
    init|repair|client) ;;
    *) ovpn_die "unsupported data lock: $name" ;;
  esac

  command -v flock >/dev/null 2>&1 || ovpn_die "flock is required for data-volume initialization"
  mkdir -p "$OVPN_DATA_DIR"
  lock_file="$OVPN_DATA_DIR/.ovpn-data.lock"
  (
    umask 077
    : >>"$lock_file"
  )
  chmod 600 "$lock_file"
  (
    flock -x 9
    "$@"
  ) 9<>"$lock_file"
}
