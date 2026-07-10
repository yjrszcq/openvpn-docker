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

  unset OPENVPN_SUPPORTED_MIN OPENVPN_SUPPORTED_MAX_EXCLUSIVE
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
}
