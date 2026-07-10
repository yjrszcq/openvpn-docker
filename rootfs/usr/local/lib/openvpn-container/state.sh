#!/usr/bin/env bash

ovpn_empty_dir_entry_is_ignored() {
  case "$1" in
    lost+found|.DS_Store)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

ovpn_data_dir_first_nonempty_entry() (
  local entry name

  if [ ! -e "$OVPN_DATA_DIR" ]; then
    return 0
  fi
  if [ ! -d "$OVPN_DATA_DIR" ] || [ ! -r "$OVPN_DATA_DIR" ] || [ ! -x "$OVPN_DATA_DIR" ]; then
    printf '%s\n' "$OVPN_DATA_DIR"
    return 0
  fi

  shopt -s dotglob nullglob
  for entry in "$OVPN_DATA_DIR"/*; do
    name="${entry##*/}"
    if ! ovpn_empty_dir_entry_is_ignored "$name"; then
      printf '%s\n' "$entry"
      return 0
    fi
  done
)

ovpn_data_dir_is_empty() {
  [ -z "$(ovpn_data_dir_first_nonempty_entry)" ]
}

ovpn_initialization_transaction_present() {
  [ -e "$OVPN_DATA_DIR/.init-transaction" ]
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

  if ovpn_initialization_transaction_present; then
    printf 'DEGRADED\n'
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
