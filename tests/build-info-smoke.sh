#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GENERATOR="$ROOT_DIR/scripts/generate-build-info.sh"
output_path="$(mktemp "${TMPDIR:-/tmp}/openvpn-build-info.XXXXXX")"
trap 'rm -f "$output_path"' EXIT

set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a

OVPN_RUNTIME_STRATEGY=debian-package-phase1 \
OVPN_RUNTIME_OPENVPN_VERSION=system \
OVPN_VCS_REF=test-revision \
OVPN_BUILD_DATE=1970-01-01T00:00:00Z \
"$GENERATOR" "$output_path"

grep -Fq "\"image_version\": \"$IMAGE_VERSION\"" "$output_path"
grep -Fq '"runtime_strategy": "debian-package-phase1"' "$output_path"
grep -Fq '"openvpn_version": "system"' "$output_path"
grep -Fq "\"openvpn_source_version\": \"$OPENVPN_VERSION\"" "$output_path"
grep -Fq "\"openvpn_source_sha256\": \"$OPENVPN_SOURCE_SHA256\"" "$output_path"
grep -Fq "\"easy_rsa_version\": \"$EASYRSA_VERSION\"" "$output_path"
grep -Fq "\"supported_openvpn_range\": \"$OPENVPN_SUPPORTED_RANGE\"" "$output_path"
grep -Fq "\"base_image\": \"$BASE_IMAGE\"" "$output_path"
grep -Fq '"vcs_revision": "test-revision"' "$output_path"
grep -Fq '"build_date": "1970-01-01T00:00:00Z"' "$output_path"

if ! [[ "$OPENVPN_SOURCE_SHA256" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo 'OPENVPN_SOURCE_SHA256 must be a SHA-256 value' >&2
  exit 1
fi

printf 'build-info smoke passed\n'
