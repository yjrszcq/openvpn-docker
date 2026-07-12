#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="$(mktemp -d "${TMPDIR:-/tmp}/openvpn-source.XXXXXX")"
trap 'rm -rf "$output_dir"' EXIT

set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a

assert_archive() {
  local archive_path="$1"
  local expected_path="$2"
  local actual_sha256

  if [ "$archive_path" != "$expected_path" ] || [ ! -f "$archive_path" ]; then
    echo 'source fetch did not return the expected archive path' >&2
    exit 1
  fi

  actual_sha256="$(sha256sum "$archive_path" | awk '{print $1}')"
  if [ "$actual_sha256" != "$OPENVPN_SOURCE_SHA256" ]; then
    echo 'downloaded source checksum does not match versions.env' >&2
    exit 1
  fi
}

archive_path="$("$ROOT_DIR/scripts/fetch-openvpn-source.sh" "$output_dir")"
assert_archive "$archive_path" "$output_dir/openvpn-$OPENVPN_VERSION.tar.gz"

fallback_dir="$output_dir/fallback"
primary_failure_curl="$output_dir/primary-failure-curl"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'for argument in "$@"; do' \
  '  case "$argument" in' \
  '    https://swupdate.openvpn.org/*) exit 1 ;;' \
  '  esac' \
  'done' \
  'exec curl "$@"' >"$primary_failure_curl"
chmod +x "$primary_failure_curl"
archive_path="$(CURL_BIN="$primary_failure_curl" "$ROOT_DIR/scripts/fetch-openvpn-source.sh" "$fallback_dir")"
assert_archive "$archive_path" "$fallback_dir/openvpn-$OPENVPN_VERSION.tar.gz"

printf 'source fetch smoke passed\n'
