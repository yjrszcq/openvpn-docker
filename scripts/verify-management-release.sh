#!/usr/bin/env bash
set -euo pipefail

RELEASE_DIR=
PUBLIC_KEY=

die() {
  printf 'verify-management-release: %s\n' "$*" >&2
  exit 74
}

while [ "$#" -gt 0 ]; do
  case "$1" in
  --release-dir)
    [ "$#" -ge 2 ] || die '--release-dir requires a value'
    RELEASE_DIR="$2"
    shift 2
    ;;
  --public-key)
    [ "$#" -ge 2 ] || die '--public-key requires a value'
    PUBLIC_KEY="$2"
    shift 2
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[ -d "$RELEASE_DIR" ] || die '--release-dir must name a directory'
[ -f "$PUBLIC_KEY" ] || die '--public-key must name a readable public key'
MANIFEST="$RELEASE_DIR/management-release.env"
SIGNATURE="$RELEASE_DIR/management-release.env.sig"
[ -f "$MANIFEST" ] || die 'manifest is missing'
[ -f "$SIGNATURE" ] || die 'manifest signature is missing'

expected_keys='FORMAT_VERSION MANAGEMENT_VERSION VCS_REF DATA_SCHEMA PLATFORM_API_MIN PLATFORM_API_MAX OPENVPN_MIN OPENVPN_MAX_EXCLUSIVE ASSET_NAME ASSET_SHA256'
actual_keys="$(awk -F= 'NF == 2 && $1 ~ /^[A-Z][A-Z0-9_]*$/ { print $1 }' "$MANIFEST" | paste -sd' ' -)"
[ "$actual_keys" = "$expected_keys" ] || die 'manifest keys or ordering are invalid'
[ "$(wc -l <"$MANIFEST" | tr -d ' ')" -eq 10 ] || die 'manifest must contain exactly ten lines'

while IFS='=' read -r key value; do
  [ -n "$value" ] || die "manifest value is empty: $key"
  case "$value" in
  *[!A-Za-z0-9._+-]*) die "manifest value contains unsafe characters: $key" ;;
  esac
  printf -v "$key" '%s' "$value"
done <"$MANIFEST"

[ "$FORMAT_VERSION" = 1 ] || die 'unsupported manifest format'
[[ "$MANAGEMENT_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'invalid management version'
[[ "$VCS_REF" =~ ^[0-9a-f]{40}$ ]] || die 'invalid VCS reference'
[[ "$DATA_SCHEMA" =~ ^[1-9][0-9]*$ ]] || die 'invalid data schema'
[[ "$PLATFORM_API_MIN" =~ ^[1-9][0-9]*$ ]] || die 'invalid platform API minimum'
[[ "$PLATFORM_API_MAX" =~ ^[1-9][0-9]*$ ]] || die 'invalid platform API maximum'
[[ "$OPENVPN_MIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'invalid OpenVPN minimum'
[[ "$OPENVPN_MAX_EXCLUSIVE" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'invalid OpenVPN maximum'
[ "$ASSET_NAME" = management-bundle.tar.gz ] || die 'unexpected asset name'
[[ "$ASSET_SHA256" =~ ^[0-9a-f]{64}$ ]] || die 'invalid asset SHA-256'

openssl pkeyutl -verify -rawin -pubin -inkey "$PUBLIC_KEY" \
  -in "$MANIFEST" -sigfile "$SIGNATURE" >/dev/null 2>&1 ||
  die 'manifest signature verification failed'

ASSET="$RELEASE_DIR/$ASSET_NAME"
[ -f "$ASSET" ] || die 'bundle asset is missing'
printf '%s  %s\n' "$ASSET_SHA256" "$ASSET" | sha256sum -c - >/dev/null 2>&1 ||
  die 'bundle SHA-256 verification failed'

expected_contract="$(
  cat <<EOF
FORMAT_VERSION=$FORMAT_VERSION
MANAGEMENT_VERSION=$MANAGEMENT_VERSION
VCS_REF=$VCS_REF
DATA_SCHEMA=$DATA_SCHEMA
PLATFORM_API_MIN=$PLATFORM_API_MIN
PLATFORM_API_MAX=$PLATFORM_API_MAX
OPENVPN_MIN=$OPENVPN_MIN
OPENVPN_MAX_EXCLUSIVE=$OPENVPN_MAX_EXCLUSIVE
EOF
)"
bundle_contract="$(tar -xOzf "$ASSET" ./management.env 2>/dev/null)" ||
  die 'bundle compatibility contract is missing'
[ "$bundle_contract" = "$expected_contract" ] ||
  die 'bundle compatibility contract does not match signed manifest'

printf 'management release verified: %s\n' "$MANAGEMENT_VERSION"
