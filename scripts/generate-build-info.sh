#!/usr/bin/env bash
set -euo pipefail

output_path="${1:?usage: generate-build-info.sh OUTPUT_PATH}"

require_value() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    printf 'missing required build metadata: %s\n' "$name" >&2
    exit 64
  fi
}

json_string() {
  local value="$1"
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '"%s"' "$value"
}

for name in \
  IMAGE_VERSION \
  BASE_IMAGE \
  OPENVPN_VERSION \
  OPENVPN_SOURCE_SHA256 \
  EASYRSA_VERSION \
  OPENVPN_SUPPORTED_RANGE; do
  require_value "$name"
done

runtime_strategy="${OVPN_RUNTIME_STRATEGY:-unknown}"
runtime_openvpn_version="${OVPN_RUNTIME_OPENVPN_VERSION:-unknown}"
vcs_ref="${OVPN_VCS_REF:-unknown}"
build_date="${OVPN_BUILD_DATE:-unknown}"

mkdir -p "$(dirname "$output_path")"
{
  printf '{\n'
  printf '  "image_version": %s,\n' "$(json_string "$IMAGE_VERSION")"
  printf '  "runtime_strategy": %s,\n' "$(json_string "$runtime_strategy")"
  printf '  "openvpn_version": %s,\n' "$(json_string "$runtime_openvpn_version")"
  printf '  "openvpn_source_version": %s,\n' "$(json_string "$OPENVPN_VERSION")"
  printf '  "openvpn_source_sha256": %s,\n' "$(json_string "$OPENVPN_SOURCE_SHA256")"
  printf '  "easy_rsa_version": %s,\n' "$(json_string "$EASYRSA_VERSION")"
  printf '  "supported_openvpn_range": %s,\n' "$(json_string "$OPENVPN_SUPPORTED_RANGE")"
  printf '  "base_image": %s,\n' "$(json_string "$BASE_IMAGE")"
  printf '  "vcs_revision": %s,\n' "$(json_string "$vcs_ref")"
  printf '  "build_date": %s\n' "$(json_string "$build_date")"
  printf '}\n'
} >"$output_path"
