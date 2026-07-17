#!/usr/bin/env bash

ovpn_layout_create() {
  mkdir -p \
    "$OVPN_DATA_DIR/config" \
    "$OVPN_DATA_DIR/meta" \
    "$OVPN_DATA_DIR/data" \
    "$OVPN_DATA_DIR/server" \
    "$OVPN_DATA_DIR/pki" \
    "$OVPN_DATA_DIR/secrets" \
    "$OVPN_DATA_DIR/clients/active" \
    "$OVPN_DATA_DIR/clients/revoked" \
    "$OVPN_DATA_DIR/clients/archive" \
    "$OVPN_DATA_DIR/ccd" \
    "$OVPN_DATA_DIR/repair/journal" \
    "$OVPN_DATA_DIR/repair/snapshots"
  chmod 750 "$OVPN_DATA_DIR" "$OVPN_DATA_DIR/config" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/data" "$OVPN_DATA_DIR/server" "$OVPN_DATA_DIR/pki" "$OVPN_DATA_DIR/secrets"
}

ovpn_init_write_transaction_marker() {
  local transaction_file="$1"
  local transaction_id="$2"
  local transaction_tmp="${transaction_file}.tmp"

  umask 077
  cat >"$transaction_tmp" <<EOF
transaction_id=$transaction_id
started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
  mv "$transaction_tmp" "$transaction_file"
  chmod 600 "$transaction_file"
}

ovpn_init_inner() {
  local final_data_dir="$OVPN_DATA_DIR"
  local transaction_id stage_dir transaction_file transaction_tmp state entry commit_started=false

  transaction_id="$(ovpn_instance_id)" || ovpn_die "failed to generate instance id"
  stage_dir="$final_data_dir/.staging-init-$transaction_id"
  transaction_file="$final_data_dir/.init-transaction"
  transaction_tmp="${transaction_file}.tmp"

  mkdir -p "$final_data_dir"
  state="$(ovpn_state_detect)"
  if [ "$state" != EMPTY ]; then
    ovpn_die "refusing to initialize non-empty data directory; current state is $state"
  fi

  mkdir "$stage_dir" || ovpn_die "failed to create init staging directory"
  cleanup_stage() {
    local status=$?
    if [ "$status" -ne 0 ]; then
      if [ "$commit_started" = true ]; then
        for entry in ccd clients config data meta pki repair secrets server; do
          [ -e "$final_data_dir/$entry" ] || continue
          [ -e "$stage_dir/$entry" ] && continue
          mv "$final_data_dir/$entry" "$stage_dir/$entry" 2>/dev/null || true
        done
      fi
      rm -rf "$stage_dir"
      if [ "$commit_started" = false ]; then
        rm -f "$transaction_file" "$transaction_tmp"
      fi
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
  ovpn_registry_initialize_empty
  ovpn_pki_init
  ovpn_tls_crypt_generate
  ovpn_render_server --output "$OVPN_DATA_DIR/server/server.conf"
  ovpn_metadata_write
  ovpn_require_healthy_state

  ovpn_init_write_transaction_marker "$transaction_file" "$transaction_id"
  commit_started=true
  for entry in ccd clients config data meta pki repair secrets server; do
    mv "$stage_dir/$entry" "$final_data_dir/$entry" || ovpn_die "failed to move $entry into place"
  done
  OVPN_DATA_DIR="$final_data_dir"
  OVPN_CONFIG_DIR="$OVPN_DATA_DIR/config"
  OVPN_PROJECT_ENV="$OVPN_CONFIG_DIR/project.env"
  OVPN_SCHEMA_VERSION_FILE="$OVPN_CONFIG_DIR/schema-version"
  unset OVPN_RENDER_DATA_DIR OVPN_INSTANCE_DATA_DIR
  rm -f "$transaction_file"
  ovpn_require_healthy_state
  commit_started=false
  trap - EXIT
  rmdir "$stage_dir"
  ovpn_log "initialized OpenVPN data directory at $OVPN_DATA_DIR"
}

ovpn_init_command() {
  ovpn_with_data_lock init ovpn_init_inner
}
