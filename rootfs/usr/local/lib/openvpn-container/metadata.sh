#!/usr/bin/env bash

ovpn_json_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  printf '%s' "$value"
}

ovpn_instance_id() {
  if [ -r /proc/sys/kernel/random/uuid ]; then
    cat /proc/sys/kernel/random/uuid
    return 0
  fi
  openssl rand -hex 16
}

ovpn_ca_fingerprint() {
  local ca="$OVPN_DATA_DIR/pki/ca.crt"
  local output
  if output="$(openssl x509 -in "$ca" -noout -fingerprint -sha256 2>/dev/null)"; then
    printf '%s\n' "${output#*=}"
    return 0
  fi
  printf 'unavailable\n'
}

ovpn_metadata_write() {
  local data_dir="${OVPN_INSTANCE_DATA_DIR:-$OVPN_DATA_DIR}"
  local instance_id created_at ca_fingerprint
  mkdir -p "$OVPN_DATA_DIR/meta"
  instance_id="$(ovpn_instance_id)"
  created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ca_fingerprint="$(ovpn_ca_fingerprint)"

  cat >"$OVPN_DATA_DIR/meta/instance.json.tmp" <<EOF
{
  "schema_version": 1,
  "instance_id": "$(ovpn_json_escape "$instance_id")",
  "initialized_at": "$(ovpn_json_escape "$created_at")",
  "server_name": "$(ovpn_json_escape "$OVPN_SERVER_NAME")",
  "data_dir": "$(ovpn_json_escape "$data_dir")",
  "ca_fingerprint_sha256": "$(ovpn_json_escape "$ca_fingerprint")"
}
EOF
  mv "$OVPN_DATA_DIR/meta/instance.json.tmp" "$OVPN_DATA_DIR/meta/instance.json"
  chmod 600 "$OVPN_DATA_DIR/meta/instance.json"
}
