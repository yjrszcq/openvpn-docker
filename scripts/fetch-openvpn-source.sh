#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${1:?usage: fetch-openvpn-source.sh OUTPUT_DIR}"

if [ -r "$ROOT_DIR/versions.env" ]; then
  set -a
  # shellcheck source=../versions.env
  . "$ROOT_DIR/versions.env"
  set +a
fi

: "${OPENVPN_VERSION:?OPENVPN_VERSION is required}"
: "${OPENVPN_SOURCE_SHA256:?OPENVPN_SOURCE_SHA256 is required}"

if ! [[ "$OPENVPN_SOURCE_SHA256" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo 'OPENVPN_SOURCE_SHA256 must be a SHA-256 value' >&2
  exit 64
fi

curl_bin="${CURL_BIN:-curl}"
archive_name="openvpn-$OPENVPN_VERSION.tar.gz"
source_url="https://swupdate.openvpn.org/community/releases/$archive_name"
archive_path="$output_dir/$archive_name"

mkdir -p "$output_dir"
temporary_path="$(mktemp "$output_dir/.${archive_name}.XXXXXX")"
cleanup() {
  rm -f "$temporary_path"
}
trap cleanup EXIT

"$curl_bin" \
  --fail \
  --location \
  --retry 3 \
  --silent \
  --show-error \
  --output "$temporary_path" \
  "$source_url"

printf '%s  %s\n' "$OPENVPN_SOURCE_SHA256" "$temporary_path" | sha256sum --check --status
mv "$temporary_path" "$archive_path"
trap - EXIT

printf '%s\n' "$archive_path"
