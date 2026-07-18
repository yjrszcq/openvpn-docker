#!/usr/bin/env bash
set -euo pipefail

RELEASE_DIR=
PUBLIC_KEY=
KEYRING=
MANIFEST_ONLY=false

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
  --keyring)
    [ "$#" -ge 2 ] || die '--keyring requires a value'
    KEYRING="$2"
    shift 2
    ;;
  --manifest-only)
    MANIFEST_ONLY=true
    shift
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[ -d "$RELEASE_DIR" ] || die '--release-dir must name a directory'
if [ -n "$PUBLIC_KEY" ] && [ -n "$KEYRING" ]; then
  die 'choose either --public-key or --keyring'
fi
if [ -n "$PUBLIC_KEY" ]; then
  [ -f "$PUBLIC_KEY" ] || die '--public-key must name a readable public key'
elif [ -n "$KEYRING" ]; then
  [ -d "$KEYRING" ] || die '--keyring must name a directory'
else
  die '--public-key or --keyring is required'
fi

MANIFEST="$RELEASE_DIR/management-release.env"
SIGNATURE="$RELEASE_DIR/management-release.env.sig"
[ -f "$MANIFEST" ] || die 'manifest is missing'
[ -f "$SIGNATURE" ] || die 'manifest signature is missing'

expected_keys='FORMAT_VERSION MANAGEMENT_VERSION VCS_REF DATA_SCHEMA PLATFORM_API_MIN PLATFORM_API_MAX OPENVPN_MIN OPENVPN_MAX_EXCLUSIVE REQUIRED_FEATURES ASSET_NAME ASSET_SHA256'
actual_keys="$(awk -F= 'NF == 2 && $1 ~ /^[A-Z][A-Z0-9_]*$/ { print $1 }' "$MANIFEST" | paste -sd' ' -)"
[ "$actual_keys" = "$expected_keys" ] || die 'manifest keys or ordering are invalid'
[ "$(wc -l <"$MANIFEST" | tr -d ' ')" -eq 11 ] || die 'manifest must contain exactly eleven lines'

while IFS='=' read -r key value; do
  [ -n "$value" ] || die "manifest value is empty: $key"
  case "$value" in
  *[!A-Za-z0-9._+,-]*) die "manifest value contains unsafe characters: $key" ;;
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
[[ "$REQUIRED_FEATURES" =~ ^[A-Za-z0-9][A-Za-z0-9,-]*$ ]] || die 'invalid required features'
[ "$PLATFORM_API_MIN" -le "$PLATFORM_API_MAX" ] || die 'platform API range is empty'
if [ "$OPENVPN_MIN" = "$OPENVPN_MAX_EXCLUSIVE" ] ||
  [ "$(printf '%s\n%s\n' "$OPENVPN_MIN" "$OPENVPN_MAX_EXCLUSIVE" | sort -V | head -1)" != "$OPENVPN_MIN" ]; then
  die 'OpenVPN range is empty or reversed'
fi
[ "$ASSET_NAME" = management-bundle.tar.gz ] || die 'unexpected asset name'
[[ "$ASSET_SHA256" =~ ^[0-9a-f]{64}$ ]] || die 'invalid asset SHA-256'

verify_signature() {
  local key="$1"
  openssl pkeyutl -verify -rawin -pubin -inkey "$key" \
    -in "$MANIFEST" -sigfile "$SIGNATURE" >/dev/null 2>&1
}

signature_valid=false
if [ -n "$PUBLIC_KEY" ]; then
  verify_signature "$PUBLIC_KEY" && signature_valid=true
else
  for key_file in "$KEYRING"/*.pem; do
    [ -f "$key_file" ] || continue
    if verify_signature "$key_file"; then
      signature_valid=true
      break
    fi
  done
fi
[ "$signature_valid" = true ] || die 'manifest signature verification failed'

if [ "$MANIFEST_ONLY" = true ]; then
  printf 'management manifest verified: %s\n' "$MANAGEMENT_VERSION"
  exit 0
fi

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
REQUIRED_FEATURES=$REQUIRED_FEATURES
EOF
)"
bundle_contract="$(tar -xOzf "$ASSET" ./management.env 2>/dev/null)" ||
  die 'bundle compatibility contract is missing'
[ "$bundle_contract" = "$expected_contract" ] ||
  die 'bundle compatibility contract does not match signed manifest'

printf 'management release verified: %s\n' "$MANAGEMENT_VERSION"
