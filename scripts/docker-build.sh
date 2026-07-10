#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a

for name in \
  IMAGE_VERSION \
  BASE_IMAGE \
  OPENVPN_VERSION \
  OPENVPN_SOURCE_SHA256 \
  EASYRSA_VERSION \
  OPENVPN_SUPPORTED_RANGE; do
  if [ -z "${!name:-}" ]; then
    printf 'missing required version input: %s\n' "$name" >&2
    exit 64
  fi
done

vcs_ref="${VCS_REF:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
build_http_proxy="${OVPN_BUILD_HTTP_PROXY:-}"
build_https_proxy="${OVPN_BUILD_HTTPS_PROXY:-}"
build_no_proxy="${OVPN_BUILD_NO_PROXY:-}"
build_network="${OVPN_BUILD_NETWORK:-default}"

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
  --build-arg "OPENVPN_VERSION=$OPENVPN_VERSION" \
  --build-arg "OPENVPN_SOURCE_SHA256=$OPENVPN_SOURCE_SHA256" \
  --build-arg "EASYRSA_VERSION=$EASYRSA_VERSION" \
  --build-arg "OPENVPN_SUPPORTED_RANGE=$OPENVPN_SUPPORTED_RANGE" \
  --build-arg "VCS_REF=$vcs_ref" \
  --build-arg "BUILD_DATE=$build_date" \
  "$@"
