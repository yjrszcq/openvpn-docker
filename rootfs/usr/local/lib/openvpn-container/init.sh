#!/usr/bin/env bash

ovpn_layout_create() {
  mkdir -p \
    "$OVPN_DATA_DIR/config" \
    "$OVPN_DATA_DIR/meta" \
    "$OVPN_DATA_DIR/server" \
    "$OVPN_DATA_DIR/pki" \
    "$OVPN_DATA_DIR/secrets" \
    "$OVPN_DATA_DIR/clients/active" \
    "$OVPN_DATA_DIR/clients/revoked" \
    "$OVPN_DATA_DIR/clients/archive" \
    "$OVPN_DATA_DIR/ccd" \
    "$OVPN_DATA_DIR/repair/journal" \
    "$OVPN_DATA_DIR/repair/snapshots"
  chmod 750 "$OVPN_DATA_DIR" "$OVPN_DATA_DIR/config" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/server" "$OVPN_DATA_DIR/pki" "$OVPN_DATA_DIR/secrets"
}

ovpn_init_inner() {
  local final_data_dir="$OVPN_DATA_DIR"
  local stage_dir="$final_data_dir/.staging-init-$$"
  local state entry

  mkdir -p "$final_data_dir"
  state="$(ovpn_state_detect)"
  if [ "$state" != EMPTY ]; then
    ovpn_die "refusing to initialize non-empty data directory; current state is $state"
  fi

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"
  cleanup_stage() {
    local status=$?
    if [ "$status" -ne 0 ]; then
      rm -rf "$stage_dir"
    fi
    return "$status"
  }
  trap cleanup_stage EXIT

  OVPN_DATA_DIR="$stage_dir"
  OVPN_CONFIG_DIR="$OVPN_DATA_DIR/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  OVPN_RENDER_DATA_DIR="$final_data_dir"
  OVPN_INSTANCE_DATA_DIR="$final_data_dir"

  ovpn_layout_create
  ovpn_config_write
  ovpn_pki_init
  ovpn_tls_crypt_generate
  ovpn_render_server --output "$OVPN_DATA_DIR/server/server.conf"
  ovpn_metadata_write
  ovpn_require_healthy_state

  for entry in ccd clients config meta pki repair secrets server; do
    mv "$stage_dir/$entry" "$final_data_dir/$entry"
  done
  rmdir "$stage_dir"
  trap - EXIT

  OVPN_DATA_DIR="$final_data_dir"
  OVPN_CONFIG_DIR="$OVPN_DATA_DIR/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  unset OVPN_RENDER_DATA_DIR OVPN_INSTANCE_DATA_DIR
  ovpn_require_healthy_state
  ovpn_log "initialized OpenVPN data directory at $OVPN_DATA_DIR"
}

ovpn_init_command() {
  ovpn_with_lock init ovpn_init_inner
}
