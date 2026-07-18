#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR=
SIGNING_KEY=
VCS_REF=
SOURCE_EPOCH="${SOURCE_DATE_EPOCH:-0}"

usage() {
  cat <<'EOF'
Usage: package-management-release.sh --output-dir DIR --signing-key FILE --vcs-ref COMMIT

Build a deterministic management bundle, strict release manifest, and Ed25519
signature. SOURCE_DATE_EPOCH may be set to control archive timestamps.
EOF
}

die() {
  printf 'package-management-release: %s\n' "$*" >&2
  exit 64
}

while [ "$#" -gt 0 ]; do
  case "$1" in
  --output-dir)
    [ "$#" -ge 2 ] || die '--output-dir requires a value'
    OUTPUT_DIR="$2"
    shift 2
    ;;
  --signing-key)
    [ "$#" -ge 2 ] || die '--signing-key requires a value'
    SIGNING_KEY="$2"
    shift 2
    ;;
  --vcs-ref)
    [ "$#" -ge 2 ] || die '--vcs-ref requires a value'
    VCS_REF="$2"
    shift 2
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[ -n "$OUTPUT_DIR" ] || die '--output-dir is required'
[ -f "$SIGNING_KEY" ] || die '--signing-key must name a readable private key'
[[ "$VCS_REF" =~ ^[0-9a-f]{40}$ ]] || die '--vcs-ref must be a full lowercase commit hash'
[[ "$SOURCE_EPOCH" =~ ^[0-9]+$ ]] || die 'SOURCE_DATE_EPOCH must be a non-negative integer'

# shellcheck disable=SC1091
. "$ROOT_DIR/versions.env"
# shellcheck disable=SC1091
. "$ROOT_DIR/compatibility/contract.env"

[[ "$MANAGEMENT_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'MANAGEMENT_VERSION must use numeric major.minor.patch form'
[[ "$PLATFORM_API" =~ ^[1-9][0-9]*$ ]] || die 'PLATFORM_API must be a positive integer'
[[ "$DATA_SCHEMA" =~ ^[1-9][0-9]*$ ]] || die 'DATA_SCHEMA must be a positive integer'
[[ "$OPENVPN_SUPPORTED_MIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'OPENVPN_SUPPORTED_MIN is invalid'
[[ "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'OPENVPN_SUPPORTED_MAX_EXCLUSIVE is invalid'

openssl pkey -in "$SIGNING_KEY" -text_pub -noout 2>/dev/null | grep -Fq 'ED25519' ||
  die 'signing key must be an Ed25519 private key'

mkdir -p "$OUTPUT_DIR"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
BUNDLE_ROOT="$WORK_DIR/bundle"
mkdir -p "$BUNDLE_ROOT/lib" "$BUNDLE_ROOT/templates" "$BUNDLE_ROOT/compatibility"
cp -a "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/." "$BUNDLE_ROOT/lib/"
cp -a "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates/." "$BUNDLE_ROOT/templates/"
cp -a "$ROOT_DIR/compatibility/." "$BUNDLE_ROOT/compatibility/"

cat >"$BUNDLE_ROOT/management.env" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=$MANAGEMENT_VERSION
VCS_REF=$VCS_REF
DATA_SCHEMA=$DATA_SCHEMA
PLATFORM_API_MIN=$PLATFORM_API
PLATFORM_API_MAX=$PLATFORM_API
OPENVPN_MIN=$OPENVPN_SUPPORTED_MIN
OPENVPN_MAX_EXCLUSIVE=$OPENVPN_SUPPORTED_MAX_EXCLUSIVE
REQUIRED_FEATURES=$OPENVPN_REQUIRED_FEATURES
EOF

find "$BUNDLE_ROOT" -type d -exec chmod 0755 {} +
find "$BUNDLE_ROOT" -type f -exec chmod 0644 {} +
find "$BUNDLE_ROOT/lib" -type f -name '*.sh' -exec chmod 0755 {} +

ASSET_NAME=management-bundle.tar.gz
ASSET="$OUTPUT_DIR/$ASSET_NAME"
MANIFEST="$OUTPUT_DIR/management-release.env"
SIGNATURE="$OUTPUT_DIR/management-release.env.sig"

tar --sort=name \
  --format=ustar \
  --mtime="@$SOURCE_EPOCH" \
  --owner=0 --group=0 --numeric-owner \
  -C "$BUNDLE_ROOT" -cf - . | gzip -n -9 >"$ASSET"
ASSET_SHA256="$(sha256sum "$ASSET" | awk '{print $1}')"

cat >"$MANIFEST" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=$MANAGEMENT_VERSION
VCS_REF=$VCS_REF
DATA_SCHEMA=$DATA_SCHEMA
PLATFORM_API_MIN=$PLATFORM_API
PLATFORM_API_MAX=$PLATFORM_API
OPENVPN_MIN=$OPENVPN_SUPPORTED_MIN
OPENVPN_MAX_EXCLUSIVE=$OPENVPN_SUPPORTED_MAX_EXCLUSIVE
REQUIRED_FEATURES=$OPENVPN_REQUIRED_FEATURES
ASSET_NAME=$ASSET_NAME
ASSET_SHA256=$ASSET_SHA256
EOF

openssl pkeyutl -sign -rawin -inkey "$SIGNING_KEY" -in "$MANIFEST" -out "$SIGNATURE"
chmod 0644 "$ASSET" "$MANIFEST" "$SIGNATURE"

printf '%s\n' "$ASSET" "$MANIFEST" "$SIGNATURE"
