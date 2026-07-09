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
