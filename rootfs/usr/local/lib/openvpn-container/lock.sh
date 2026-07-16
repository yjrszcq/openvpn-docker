#!/usr/bin/env bash

ovpn_with_data_lock() {
  local name="$1"
  shift
  local lock_file

  case "$name" in
    init|registry|repair|client) ;;
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
    flock -x -w 30 9 || ovpn_die "failed to acquire data lock (timeout 30s)"
    "$@"
  ) 9<>"$lock_file"
}
