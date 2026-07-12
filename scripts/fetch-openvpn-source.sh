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
archive_path="$output_dir/$archive_name"
source_urls=(
  "https://swupdate.openvpn.org/community/releases/$archive_name"
  "https://github.com/OpenVPN/openvpn/releases/download/v$OPENVPN_VERSION/$archive_name"
)
connect_timeout="${OPENVPN_FETCH_CONNECT_TIMEOUT:-10}"
max_time="${OPENVPN_FETCH_MAX_TIME:-60}"

mkdir -p "$output_dir"
temporary_path="$(mktemp "$output_dir/.${archive_name}.XXXXXX")"
cleanup() {
  rm -f "$temporary_path"
}
trap cleanup EXIT

downloaded=false
for source_url in "${source_urls[@]}"; do
  printf 'fetching OpenVPN source from %s\n' "$source_url" >&2
  if "$curl_bin" \
    --fail \
    --location \
    --retry 3 \
    --retry-max-time "$max_time" \
    --connect-timeout "$connect_timeout" \
    --max-time "$max_time" \
    --silent \
    --show-error \
    --output "$temporary_path" \
    "$source_url"; then
    downloaded=true
    break
  fi
done

if [ "$downloaded" != true ]; then
  echo 'unable to download the pinned OpenVPN source archive from any official source' >&2
  exit 1
fi

printf '%s  %s\n' "$OPENVPN_SOURCE_SHA256" "$temporary_path" | sha256sum --check --status
mv "$temporary_path" "$archive_path"
trap - EXIT

printf '%s\n' "$archive_path"
