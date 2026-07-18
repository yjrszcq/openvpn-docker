#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR=
SIGNING_KEY=
VERSION=

while [ "$#" -gt 0 ]; do
  case "$1" in
  --output-dir)
    OUTPUT_DIR="$2"
    shift 2
    ;;
  --signing-key)
    SIGNING_KEY="$2"
    shift 2
    ;;
  --version)
    VERSION="$2"
    shift 2
    ;;
  *)
    printf 'unknown fixture argument: %s\n' "$1" >&2
    exit 64
    ;;
  esac
done

[ -n "$OUTPUT_DIR" ] && [ -f "$SIGNING_KEY" ] &&
  [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 64

# shellcheck source=../../versions.env
. "$ROOT_DIR/versions.env"
# shellcheck source=../../compatibility/contract.env
. "$ROOT_DIR/compatibility/contract.env"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
bundle="$work/bundle"
mkdir -p "$OUTPUT_DIR" "$bundle/lib" "$bundle/templates" "$bundle/compatibility"
cp -a "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/." "$bundle/lib/"
cp -a "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates/." "$bundle/templates/"
cp -a "$ROOT_DIR/compatibility/." "$bundle/compatibility/"

cat >"$bundle/management.env" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=$VERSION
VCS_REF=0123456789abcdef0123456789abcdef01234567
DATA_SCHEMA=$DATA_SCHEMA
PLATFORM_API_MIN=$PLATFORM_API
PLATFORM_API_MAX=$PLATFORM_API
OPENVPN_SUPPORTED_VERSIONS=$OPENVPN_SUPPORTED_VERSIONS
REQUIRED_FEATURES=$OPENVPN_REQUIRED_FEATURES
EOF
find "$bundle" -type d -exec chmod 0755 {} +
find "$bundle" -type f -exec chmod 0644 {} +
find "$bundle/lib" -type f -name '*.sh' -exec chmod 0755 {} +

tar --sort=name --format=ustar --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -C "$bundle" -cf - . | gzip -n -9 >"$OUTPUT_DIR/management-bundle.tar.gz"
sha="$(sha256sum "$OUTPUT_DIR/management-bundle.tar.gz" | awk '{print $1}')"
cat >"$OUTPUT_DIR/management-release.env" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=$VERSION
VCS_REF=0123456789abcdef0123456789abcdef01234567
DATA_SCHEMA=$DATA_SCHEMA
PLATFORM_API_MIN=$PLATFORM_API
PLATFORM_API_MAX=$PLATFORM_API
OPENVPN_SUPPORTED_VERSIONS=$OPENVPN_SUPPORTED_VERSIONS
REQUIRED_FEATURES=$OPENVPN_REQUIRED_FEATURES
ASSET_NAME=management-bundle.tar.gz
ASSET_SHA256=$sha
EOF
openssl pkeyutl -sign -rawin -inkey "$SIGNING_KEY" \
  -in "$OUTPUT_DIR/management-release.env" -out "$OUTPUT_DIR/management-release.env.sig"
