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

exec docker build \
  --build-arg "BASE_IMAGE=$BASE_IMAGE" \
  --build-arg "IMAGE_VERSION=$IMAGE_VERSION" \
  --build-arg "OPENVPN_VERSION=$OPENVPN_VERSION" \
  --build-arg "OPENVPN_SOURCE_SHA256=$OPENVPN_SOURCE_SHA256" \
  --build-arg "EASYRSA_VERSION=$EASYRSA_VERSION" \
  --build-arg "OPENVPN_SUPPORTED_RANGE=$OPENVPN_SUPPORTED_RANGE" \
  --build-arg "VCS_REF=$vcs_ref" \
  --build-arg "BUILD_DATE=$build_date" \
  "$@"
