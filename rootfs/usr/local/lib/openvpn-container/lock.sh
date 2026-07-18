#!/usr/bin/env bash

ovpn_with_data_lock() {
  local name="$1"
  shift
  local lock_file

  case "$name" in
    init|registry|repair|client|migration) ;;
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

ovpn_runtime_lock_file() {
  printf '%s/.ovpn-runtime.lock\n' "$OVPN_DATA_DIR"
}

ovpn_runtime_lock_prepare() {
  local lock_file

  command -v flock >/dev/null 2>&1 || ovpn_die 'flock is required for runtime coordination'
  mkdir -p "$OVPN_DATA_DIR"
  lock_file="$(ovpn_runtime_lock_file)"
  (
    umask 077
    : >>"$lock_file"
  )
  chmod 600 "$lock_file"
  printf '%s\n' "$lock_file"
}

ovpn_with_runtime_shared_lock() {
  local lock_file result

  lock_file="$(ovpn_runtime_lock_prepare)"
  exec 8<>"$lock_file"
  flock -s -w 30 8 || ovpn_die 'failed to acquire shared runtime lock (timeout 30s)'
  "$@"
  result=$?
  flock -u 8 || true
  exec 8>&-
  return "$result"
}

ovpn_with_runtime_exclusive_lock() (
  local lock_file

  lock_file="$(ovpn_runtime_lock_prepare)"
  exec 8<>"$lock_file"
  flock -x -n 8 || {
    ovpn_log 'OpenVPN is running; stop the openvpn service before migration'
    return 78
  }
  "$@"
)
