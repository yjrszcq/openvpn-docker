#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSIONS_ENV="${OVPN_VERSIONS_ENV:-$ROOT_DIR/versions.env}"
CURL_BIN="${OPENVPN_UPDATE_CURL:-curl}"

usage() {
  echo 'usage: update-openvpn.sh VERSION [SOURCE_SHA256]' >&2
  exit 64
}

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  usage
fi
version="$1"
source_sha256="${2:-}"
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'VERSION must use numeric major.minor.patch form' >&2
  exit 64
fi
[ -r "$VERSIONS_ENV" ] || {
  echo "versions file is unreadable: $VERSIONS_ENV" >&2
  exit 66
}

if [ -z "$source_sha256" ]; then
  temporary_archive="$(mktemp)"
  trap 'rm -f "$temporary_archive"' EXIT
  "$CURL_BIN" --fail --location --silent --show-error \
    --output "$temporary_archive" \
    "https://github.com/OpenVPN/openvpn/releases/download/v$version/openvpn-$version.tar.gz"
  source_sha256="$(sha256sum "$temporary_archive" | awk '{print $1}')"
fi
if ! [[ "$source_sha256" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo 'SOURCE_SHA256 must be a SHA-256 value' >&2
  exit 64
fi

temporary_versions="$VERSIONS_ENV.tmp"
awk -v version="$version" -v source_sha256="$source_sha256" '
  $0 ~ /^OPENVPN_VERSION=/ {
    print "OPENVPN_VERSION=" version
    saw_version = 1
    next
  }
  $0 ~ /^OPENVPN_SOURCE_SHA256=/ {
    print "OPENVPN_SOURCE_SHA256=" source_sha256
    saw_sha256 = 1
    next
  }
  { print }
  END {
    if (!saw_version || !saw_sha256) {
      exit 1
    }
  }
' "$VERSIONS_ENV" >"$temporary_versions" || {
  rm -f "$temporary_versions"
  echo 'versions file must define OPENVPN_VERSION and OPENVPN_SOURCE_SHA256' >&2
  exit 65
}
mv "$temporary_versions" "$VERSIONS_ENV"
printf 'updated OpenVPN source to %s\n' "$version"
