#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a

for name in \
  IMAGE_VERSION \
  DATA_SCHEMA \
  BASE_IMAGE \
  OPENVPN_VERSION \
  OPENVPN_SOURCE_SHA256 \
  EASYRSA_VERSION \
  OPENVPN_CANDIDATE_RANGE; do
  if [ -z "${!name:-}" ]; then
    printf 'missing required version input: %s\n' "$name" >&2
    exit 64
  fi
done

vcs_ref="${VCS_REF:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
build_network="${OVPN_BUILD_NETWORK:-default}"

ovpn_standard_build_proxy() {
  local proxy="${1:-}"
  printf '%s\n' "$proxy"
}

if [ "${OVPN_BUILD_HTTP_PROXY+x}" = x ]; then
  build_http_proxy="$OVPN_BUILD_HTTP_PROXY"
else
  build_http_proxy="$(ovpn_standard_build_proxy "${HTTP_PROXY-${http_proxy-}}")"
fi
if [ "${OVPN_BUILD_HTTPS_PROXY+x}" = x ]; then
  build_https_proxy="$OVPN_BUILD_HTTPS_PROXY"
else
  build_https_proxy="$(ovpn_standard_build_proxy "${HTTPS_PROXY-${https_proxy-}}")"
fi
if [ "${OVPN_BUILD_NO_PROXY+x}" = x ]; then
  build_no_proxy="$OVPN_BUILD_NO_PROXY"
else
  build_no_proxy="${NO_PROXY-${no_proxy-}}"
fi

exec docker build \
  --network "$build_network" \
  --build-arg "HTTP_PROXY=$build_http_proxy" \
  --build-arg "HTTPS_PROXY=$build_https_proxy" \
  --build-arg "NO_PROXY=$build_no_proxy" \
  --build-arg "http_proxy=$build_http_proxy" \
  --build-arg "https_proxy=$build_https_proxy" \
  --build-arg "no_proxy=$build_no_proxy" \
  --build-arg "BASE_IMAGE=$BASE_IMAGE" \
  --build-arg "IMAGE_VERSION=$IMAGE_VERSION" \
  --build-arg "DATA_SCHEMA=$DATA_SCHEMA" \
  --build-arg "OPENVPN_VERSION=$OPENVPN_VERSION" \
  --build-arg "OPENVPN_SOURCE_SHA256=$OPENVPN_SOURCE_SHA256" \
  --build-arg "EASYRSA_VERSION=$EASYRSA_VERSION" \
  --build-arg "OPENVPN_CANDIDATE_RANGE=$OPENVPN_CANDIDATE_RANGE" \
  --build-arg "VCS_REF=$vcs_ref" \
  --build-arg "BUILD_DATE=$build_date" \
  "$@"
