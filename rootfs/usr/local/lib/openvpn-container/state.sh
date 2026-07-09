#!/usr/bin/env bash

ovpn_data_dir_is_empty() {
  if [ ! -d "$OVPN_DATA_DIR" ]; then
    return 0
  fi

  local entry
  entry="$(find "$OVPN_DATA_DIR" -mindepth 1 -maxdepth 1 \
    ! -name lost+found \
    ! -name .DS_Store \
    -print -quit)"
  [ -z "$entry" ]
}

ovpn_required_files() {
  cat <<EOF
$OVPN_DATA_DIR/config/project.env
$OVPN_DATA_DIR/config/schema-version
$OVPN_DATA_DIR/meta/instance.json
$OVPN_DATA_DIR/pki/ca.crt
$OVPN_DATA_DIR/pki/private/ca.key
$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt
$OVPN_DATA_DIR/pki/private/$OVPN_SERVER_NAME.key
$OVPN_DATA_DIR/pki/index.txt
$OVPN_DATA_DIR/pki/serial
$OVPN_DATA_DIR/pki/crl.pem
$OVPN_DATA_DIR/secrets/tls-crypt.key
$OVPN_DATA_DIR/server/server.conf
EOF
}

ovpn_missing_required_files() {
  local file
  ovpn_required_files | while IFS= read -r file; do
    [ -n "$file" ] || continue
    if [ ! -e "$file" ]; then
      printf '%s\n' "$file"
    fi
  done
}

ovpn_state_detect() {
  if ovpn_data_dir_is_empty; then
    printf 'EMPTY\n'
    return 0
  fi

  if [ -z "$(ovpn_missing_required_files)" ]; then
    printf 'HEALTHY\n'
    return 0
  fi

  printf 'DEGRADED\n'
}

ovpn_state_command() {
  ovpn_state_detect
}

ovpn_require_healthy_state() {
  local state
  state="$(ovpn_state_detect)"
  if [ "$state" != HEALTHY ]; then
    ovpn_log "instance state is $state"
    ovpn_missing_required_files | while IFS= read -r file; do
      [ -n "$file" ] || continue
      ovpn_log "missing required file: $file"
    done
    exit 1
  fi
}
