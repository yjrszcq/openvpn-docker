#!/usr/bin/env bash

OVPN_EVENTS_FILE="${OVPN_EVENTS_FILE:-${OVPN_DATA_DIR:-/etc/openvpn}/logs/events.jsonl}"

ovpn_event_identity_name() {
  local id="$1"
  local registry="${OVPN_DATA_DIR:-/etc/openvpn}/meta/client-state.csv"

  [ -r "$registry" ] || return 1
  awk -F, -v wanted="$id" '
    $1 == wanted && $2 != "" {
      print $2
      found++
    }
    END { if (found != 1) exit 1 }
  ' "$registry"
}

ovpn_event_write() {
  local event="$1"
  local operation="$2"
  local outcome="$3"
  local client_id="$4"
  local client_name="$5"
  local details="${6:-}"
  local timestamp record directory lock_file
  local lock_fd

  [ -n "$details" ] || details='{}'
  timestamp="${OVPN_EVENT_TIMESTAMP:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
  record="$(
    jq -cn \
      --arg timestamp "$timestamp" \
      --arg event "$event" \
      --arg operation "$operation" \
      --arg outcome "$outcome" \
      --arg client_id "$client_id" \
      --arg client_name "$client_name" \
      --argjson details "$details" '
        $details + {
          timestamp: $timestamp,
          event: $event,
          operation: $operation,
          outcome: $outcome,
          client_id: (if $client_id == "" then null else $client_id end),
          client_name: (if $client_name == "" then null else $client_name end)
        }
      '
  )" || return 1
  directory="$(dirname "$OVPN_EVENTS_FILE")"
  lock_file="$directory/.events.lock"
  mkdir -p "$directory" || return 1
  chmod 750 "$directory" || return 1
  exec {lock_fd}>"$lock_file" || return 1
  chmod 600 "$lock_file" || {
    exec {lock_fd}>&-
    return 1
  }
  flock -x "$lock_fd" || {
    exec {lock_fd}>&-
    return 1
  }
  printf '%s\n' "$record" >>"$OVPN_EVENTS_FILE" || {
    flock -u "$lock_fd" || true
    exec {lock_fd}>&-
    return 1
  }
  chmod 600 "$OVPN_EVENTS_FILE" || {
    flock -u "$lock_fd" || true
    exec {lock_fd}>&-
    return 1
  }
  flock -u "$lock_fd" || true
  exec {lock_fd}>&-
}
