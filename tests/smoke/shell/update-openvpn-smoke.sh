#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/update-openvpn.sh"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
VERSIONS_ENV="$TMP_DIR/versions.env"
CURL_LOG="$TMP_DIR/curl.log"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$FAKE_BIN"
cat >"$FAKE_BIN/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail
output=''
url=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      shift
      output="$1"
      ;;
    http*)
      url="$1"
      ;;
  esac
  shift
done
printf '%s\n' "$url" >"$OVPN_TEST_CURL_LOG"
printf '%s\n' 'official test archive' >"$output"
FAKE_CURL
chmod +x "$FAKE_BIN/curl"

cat >"$VERSIONS_ENV" <<'EOF_VERSIONS'
IMAGE_VERSION=1.2.3
BASE_IMAGE=debian:test
OPENVPN_VERSION=2.7.5
OPENVPN_SOURCE_SHA256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
EASYRSA_VERSION=system
OPENVPN_CANDIDATE_RANGE=">=2.7.0 <2.8.0"
EOF_VERSIONS

OVPN_VERSIONS_ENV="$VERSIONS_ENV" \
  OPENVPN_UPDATE_CURL="$FAKE_BIN/curl" \
  OVPN_TEST_CURL_LOG="$CURL_LOG" \
  "$SCRIPT" 2.7.6 >"$TMP_DIR/update.out"
expected_sha="$(printf '%s\n' 'official test archive' | sha256sum | awk '{print $1}')"
grep -Fqx 'OPENVPN_VERSION=2.7.6' "$VERSIONS_ENV"
grep -Fqx "OPENVPN_SOURCE_SHA256=$expected_sha" "$VERSIONS_ENV"
grep -Fqx 'https://github.com/OpenVPN/openvpn/releases/download/v2.7.6/openvpn-2.7.6.tar.gz' "$CURL_LOG"
grep -Fq 'updated OpenVPN source to 2.7.6' "$TMP_DIR/update.out"

cat >"$FAKE_BIN/fail-curl" <<'FAIL_CURL'
#!/usr/bin/env bash
exit 99
FAIL_CURL
chmod +x "$FAKE_BIN/fail-curl"
provided_sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
OVPN_VERSIONS_ENV="$VERSIONS_ENV" OPENVPN_UPDATE_CURL="$FAKE_BIN/fail-curl" "$SCRIPT" 2.7.7 "$provided_sha"
grep -Fqx 'OPENVPN_VERSION=2.7.7' "$VERSIONS_ENV"
grep -Fqx "OPENVPN_SOURCE_SHA256=$provided_sha" "$VERSIONS_ENV"

printf 'update OpenVPN smoke passed\n'
