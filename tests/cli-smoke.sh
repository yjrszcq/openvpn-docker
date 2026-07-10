#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
build_info_path="$(mktemp "${TMPDIR:-/tmp}/openvpn-cli-build-info.XXXXXX")"
trap 'rm -f "$build_info_path"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a
OVPN_RUNTIME_STRATEGY=source-build \
OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
OVPN_VCS_REF=test-revision \
OVPN_BUILD_DATE=1970-01-01T00:00:00Z \
"$ROOT_DIR/scripts/generate-build-info.sh" "$build_info_path"
export OVPN_BUILD_INFO="$build_info_path"

"$OVPN" help >/tmp/ovpn-help.out
if ! grep -q 'Usage: ovpn' /tmp/ovpn-help.out; then
  echo 'help output missing usage' >&2
  exit 1
fi

"$OVPN" version >/tmp/ovpn-version.out
if ! grep -Fq "\"image_version\": \"$IMAGE_VERSION\"" /tmp/ovpn-version.out; then
  echo 'version output missing image_version' >&2
  exit 1
fi
if ! grep -Fq "\"openvpn_source_version\": \"$OPENVPN_VERSION\"" /tmp/ovpn-version.out; then
  echo 'version output missing openvpn_source_version' >&2
  exit 1
fi

set +e
"$OVPN" doctor >/tmp/ovpn-doctor.out 2>/tmp/ovpn-doctor.err
status=$?
set -e
if [ "$status" -ne 2 ]; then
  echo "doctor returned $status, expected 2 for phase stub" >&2
  exit 1
fi
if ! grep -q "not implemented" /tmp/ovpn-doctor.err; then
  echo 'doctor stub did not explain not implemented state' >&2
  exit 1
fi

set +e
"$OVPN" does-not-exist >/tmp/ovpn-unknown.out 2>/tmp/ovpn-unknown.err
status=$?
set -e
if [ "$status" -ne 64 ]; then
  echo "unknown command returned $status, expected 64" >&2
  exit 1
fi

printf 'cli smoke passed\n'
