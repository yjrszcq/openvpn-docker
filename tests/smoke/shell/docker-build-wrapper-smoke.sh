#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
ARGS_FILE="$TMP_DIR/docker-args"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$FAKE_BIN"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'printf "%s\\n" "$@" >"$OVPN_TEST_DOCKER_ARGS"' >"$FAKE_BIN/docker"
chmod +x "$FAKE_BIN/docker"

assert_proxy_args() {
  local http_proxy="$1"
  local https_proxy="$2"
  local no_proxy="$3"

  grep -Fxq "HTTP_PROXY=$http_proxy" "$ARGS_FILE"
  grep -Fxq "HTTPS_PROXY=$https_proxy" "$ARGS_FILE"
  grep -Fxq "NO_PROXY=$no_proxy" "$ARGS_FILE"
  grep -Fxq "http_proxy=$http_proxy" "$ARGS_FILE"
  grep -Fxq "https_proxy=$https_proxy" "$ARGS_FILE"
  grep -Fxq "no_proxy=$no_proxy" "$ARGS_FILE"
}

PATH="$FAKE_BIN:$PATH" \
OVPN_TEST_DOCKER_ARGS="$ARGS_FILE" \
HTTP_PROXY=http://standard-http.example:8080 \
HTTPS_PROXY=http://standard-https.example:8443 \
NO_PROXY=localhost,127.0.0.1 \
"$ROOT_DIR/scripts/docker-build.sh" -t test/openvpn:wrapper "$ROOT_DIR"
assert_proxy_args http://standard-http.example:8080 http://standard-https.example:8443 localhost,127.0.0.1

PATH="$FAKE_BIN:$PATH" \
OVPN_TEST_DOCKER_ARGS="$ARGS_FILE" \
HTTP_PROXY=http://127.0.0.1:7890 \
HTTPS_PROXY=http://127.0.0.1:7890 \
NO_PROXY=localhost,127.0.0.1 \
"$ROOT_DIR/scripts/docker-build.sh" -t test/openvpn:wrapper "$ROOT_DIR"
assert_proxy_args '' '' localhost,127.0.0.1

PATH="$FAKE_BIN:$PATH" \
OVPN_TEST_DOCKER_ARGS="$ARGS_FILE" \
HTTP_PROXY=http://127.0.0.1:7890 \
HTTPS_PROXY=http://127.0.0.1:7890 \
NO_PROXY=localhost,127.0.0.1 \
OVPN_BUILD_NETWORK=host \
"$ROOT_DIR/scripts/docker-build.sh" -t test/openvpn:wrapper "$ROOT_DIR"
assert_proxy_args http://127.0.0.1:7890 http://127.0.0.1:7890 localhost,127.0.0.1

PATH="$FAKE_BIN:$PATH" \
OVPN_TEST_DOCKER_ARGS="$ARGS_FILE" \
HTTP_PROXY=http://standard-http.example:8080 \
HTTPS_PROXY=http://standard-https.example:8443 \
NO_PROXY=localhost,127.0.0.1 \
OVPN_BUILD_HTTP_PROXY=http://override-http.example:8080 \
OVPN_BUILD_HTTPS_PROXY=http://override-https.example:8443 \
OVPN_BUILD_NO_PROXY=internal.example \
"$ROOT_DIR/scripts/docker-build.sh" -t test/openvpn:wrapper "$ROOT_DIR"
assert_proxy_args http://override-http.example:8080 http://override-https.example:8443 internal.example

printf 'docker build wrapper smoke passed\n'
