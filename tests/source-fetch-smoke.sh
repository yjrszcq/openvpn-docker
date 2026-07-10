#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="$(mktemp -d "${TMPDIR:-/tmp}/openvpn-source.XXXXXX")"
trap 'rm -rf "$output_dir"' EXIT

set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a

archive_path="$("$ROOT_DIR/scripts/fetch-openvpn-source.sh" "$output_dir")"
expected_path="$output_dir/openvpn-$OPENVPN_VERSION.tar.gz"

if [ "$archive_path" != "$expected_path" ] || [ ! -f "$archive_path" ]; then
  echo 'source fetch did not return the expected archive path' >&2
  exit 1
fi

actual_sha256="$(sha256sum "$archive_path" | awk '{print $1}')"
if [ "$actual_sha256" != "$OPENVPN_SOURCE_SHA256" ]; then
  echo 'downloaded source checksum does not match versions.env' >&2
  exit 1
fi

printf 'source fetch smoke passed\n'
