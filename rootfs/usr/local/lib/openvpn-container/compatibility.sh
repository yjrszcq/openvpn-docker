#!/usr/bin/env bash

OVPN_COMPATIBILITY_DIR="${OVPN_COMPATIBILITY_DIR:-/usr/local/share/openvpn-container/compatibility}"
OVPN_COMPATIBILITY_CONTRACT="${OVPN_COMPATIBILITY_CONTRACT:-$OVPN_COMPATIBILITY_DIR/contract.env}"

ovpn_semver_normalize() {
  local version="$1"
  local major minor patch

  if ! [[ "$version" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    return 1
  fi

  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  patch="${BASH_REMATCH[3]}"
  printf '%d.%d.%d\n' "$((10#$major))" "$((10#$minor))" "$((10#$patch))"
}

ovpn_semver_compare() {
  local left right
  local left_major left_minor left_patch
  local right_major right_minor right_patch

  left="$(ovpn_semver_normalize "$1")" || return 1
  right="$(ovpn_semver_normalize "$2")" || return 1
  IFS=. read -r left_major left_minor left_patch <<<"$left"
  IFS=. read -r right_major right_minor right_patch <<<"$right"

  if ((left_major != right_major)); then
    ((left_major < right_major)) && printf '%s\n' -1 || printf '%s\n' 1
    return 0
  fi
  if ((left_minor != right_minor)); then
    ((left_minor < right_minor)) && printf '%s\n' -1 || printf '%s\n' 1
    return 0
  fi
  if ((left_patch != right_patch)); then
    ((left_patch < right_patch)) && printf '%s\n' -1 || printf '%s\n' 1
    return 0
  fi
  printf '%s\n' 0
}

ovpn_compatibility_load_contract() {
  [ -r "$OVPN_COMPATIBILITY_CONTRACT" ] || return 1

  unset OPENVPN_SUPPORTED_MIN OPENVPN_SUPPORTED_MAX_EXCLUSIVE OPENVPN_ADAPTER OPENVPN_TEMPLATE_FAMILY OPENVPN_REQUIRED_FEATURES
  # shellcheck source=/usr/local/share/openvpn-container/compatibility/contract.env
  . "$OVPN_COMPATIBILITY_CONTRACT"

  [ -n "${OPENVPN_SUPPORTED_MIN:-}" ] || return 1
  [ -n "${OPENVPN_SUPPORTED_MAX_EXCLUSIVE:-}" ] || return 1
  ovpn_semver_normalize "$OPENVPN_SUPPORTED_MIN" >/dev/null || return 1
  ovpn_semver_normalize "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE" >/dev/null || return 1
  [ "$(ovpn_semver_compare "$OPENVPN_SUPPORTED_MIN" "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE")" = -1 ]
}

ovpn_runtime_version() {
  local bin first_line version

  bin="$(ovpn_openvpn_bin)" || return 1
  first_line="$("$bin" --version 2>&1 | sed -n '1p')" || return 1
  if ! [[ "$first_line" =~ ^OpenVPN[[:space:]]+([0-9]+\.[0-9]+\.[0-9]+) ]]; then
    return 1
  fi
  version="${BASH_REMATCH[1]}"
  ovpn_semver_normalize "$version"
}

ovpn_compatibility_runtime_supported() {
  local runtime minimum maximum

  ovpn_compatibility_load_contract || return 1
  runtime="$(ovpn_runtime_version)" || return 1
  minimum="$(ovpn_semver_compare "$runtime" "$OPENVPN_SUPPORTED_MIN")" || return 1
  maximum="$(ovpn_semver_compare "$runtime" "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE")" || return 1
  [ "$minimum" != -1 ] && [ "$maximum" = -1 ]
}

ovpn_compatibility_require_supported() {
  local runtime

  ovpn_compatibility_load_contract || ovpn_die "invalid compatibility contract: $OVPN_COMPATIBILITY_CONTRACT"
  runtime="$(ovpn_runtime_version)" || ovpn_die "unable to parse OpenVPN runtime version"
  if ! ovpn_compatibility_runtime_supported; then
    ovpn_die "OpenVPN runtime $runtime is outside supported range [$OPENVPN_SUPPORTED_MIN, $OPENVPN_SUPPORTED_MAX_EXCLUSIVE)"
  fi
  if ! ovpn_compatibility_required_features_supported; then
    ovpn_die "OpenVPN runtime $runtime lacks required capabilities"
  fi
}

ovpn_compatibility_load_adapter() {
  local adapter_path

  ovpn_compatibility_load_contract || return 1
  case "${OPENVPN_ADAPTER:-}" in
    ''|*[!A-Za-z0-9._-]*) return 1 ;;
  esac
  adapter_path="$OVPN_COMPATIBILITY_DIR/adapters/$OPENVPN_ADAPTER.sh"
  [ -r "$adapter_path" ] || return 1

  unset OVPN_ADAPTER_NAME OVPN_ADAPTER_TEMPLATE_FAMILY
  # shellcheck source=/usr/local/share/openvpn-container/compatibility/adapters/openvpn-2.7.sh
  . "$adapter_path"
  [ "${OVPN_ADAPTER_NAME:-}" = "$OPENVPN_ADAPTER" ] || return 1
  [ "${OVPN_ADAPTER_TEMPLATE_FAMILY:-}" = "${OPENVPN_TEMPLATE_FAMILY:-}" ]
}

ovpn_compatibility_adapter_name() {
  ovpn_compatibility_runtime_supported || return 1
  ovpn_compatibility_load_adapter || return 1
  printf '%s\n' "$OVPN_ADAPTER_NAME"
}

ovpn_compatibility_template_family() {
  ovpn_compatibility_runtime_supported || return 1
  ovpn_compatibility_load_adapter || return 1
  printf '%s\n' "$OVPN_ADAPTER_TEMPLATE_FAMILY"
}

ovpn_compatibility_required_features() {
  local feature
  local -a features

  ovpn_compatibility_load_contract || return 1
  [ -n "${OPENVPN_REQUIRED_FEATURES:-}" ] || return 1
  IFS=, read -ra features <<<"$OPENVPN_REQUIRED_FEATURES"
  for feature in "${features[@]}"; do
    [[ "$feature" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || return 1
    printf '%s\n' "$feature"
  done
}

ovpn_compatibility_runtime_help() {
  local bin

  bin="$(ovpn_openvpn_bin)" || return 1
  "$bin" --help 2>&1 || true
}

ovpn_compatibility_probe_feature() {
  local feature="$1"
  local help_output="$2"

  ovpn_compatibility_load_adapter || return 1
  declare -F ovpn_adapter_probe_feature >/dev/null || return 1
  ovpn_adapter_probe_feature "$feature" "$help_output"
}

ovpn_compatibility_required_features_supported() {
  local feature_output help_output feature

  ovpn_compatibility_adapter_name >/dev/null || return 1
  feature_output="$(ovpn_compatibility_required_features)" || return 1
  [ -n "$feature_output" ] || return 1
  help_output="$(ovpn_compatibility_runtime_help)"
  while IFS= read -r feature; do
    ovpn_compatibility_probe_feature "$feature" "$help_output" || return 1
  done <<<"$feature_output"
}

ovpn_compatibility_validate_config() {
  local config_path="$1"
  local bin

  [ -r "$config_path" ] || return 1
  ovpn_compatibility_adapter_name >/dev/null || return 1
  [ -n "${OVPN_ADAPTER_CONFIG_TEST_CIPHER:-}" ] || return 1
  bin="$(ovpn_openvpn_bin)" || return 1
  "$bin" --config "$config_path" --cipher "$OVPN_ADAPTER_CONFIG_TEST_CIPHER" --test-crypto >/dev/null 2>&1
}

ovpn_capabilities_command() {
  local runtime adapter help_output feature_output feature key status range_supported
  local -a features feature_values

  ovpn_compatibility_load_contract || ovpn_die "invalid compatibility contract: $OVPN_COMPATIBILITY_CONTRACT"
  runtime="$(ovpn_runtime_version)" || ovpn_die "unable to parse OpenVPN runtime version"
  feature_output="$(ovpn_compatibility_required_features)" || ovpn_die "invalid required features in compatibility contract"
  [ -n "$feature_output" ] || ovpn_die "compatibility contract has no required features"
  mapfile -t features <<<"$feature_output"

  adapter=""
  range_supported=false
  status=0
  if ovpn_compatibility_runtime_supported; then
    range_supported=true
    adapter="$(ovpn_compatibility_adapter_name)" || status=1
  else
    status=1
  fi

  help_output=""
  if [ "$range_supported" = true ] && [ -n "$adapter" ]; then
    help_output="$(ovpn_compatibility_runtime_help)"
  fi

  for feature in "${features[@]}"; do
    if [ "$range_supported" = true ] && [ -n "$adapter" ] && ovpn_compatibility_probe_feature "$feature" "$help_output"; then
      feature_values+=(true)
    else
      feature_values+=(false)
      status=1
    fi
  done

  printf '{\n'
  printf '  "openvpn_version": "%s",\n' "$runtime"
  printf '  "supported_range": %s,\n' "$range_supported"
  if [ -n "$adapter" ]; then
    printf '  "adapter": "%s",\n' "$adapter"
  else
    printf '  "adapter": null,\n'
  fi
  printf '  "features": {\n'
  for ((index = 0; index < ${#features[@]}; index++)); do
    feature="${features[index]}"
    key="${feature//-/_}"
    printf '    "%s": %s' "$key" "${feature_values[index]}"
    if [ "$index" -lt $((${#features[@]} - 1)) ]; then
      printf ','
    fi
    printf '\n'
  done
  printf '  }\n'
  printf '}\n'
  return "$status"
}
