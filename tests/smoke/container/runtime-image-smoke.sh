#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_RUNTIME_IMAGE:-szcq/openvpn-server:runtime-smoke}"
REQUIRED="${OVPN_RUNTIME_REQUIRED:-0}"
SKIP_BUILD="${OVPN_RUNTIME_SKIP_BUILD:-0}"

set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a

skip_or_fail() {
  local reason="$1"
  if [ "$REQUIRED" = 1 ]; then
    echo "runtime image smoke failed: $reason" >&2
    exit 1
  fi
  echo "runtime image smoke skipped: $reason"
  exit 0
}

if ! command -v docker >/dev/null 2>&1; then
  skip_or_fail 'missing command: docker'
fi
if ! docker info >/dev/null 2>&1; then
  skip_or_fail 'Docker daemon is not accessible'
fi

if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
fi

version_output="$(docker run --rm --entrypoint openvpn "$IMAGE" --version)"
if ! grep -Fq "OpenVPN $OPENVPN_VERSION" <<<"$version_output"; then
  printf '%s\n' "$version_output" >&2
  echo 'runtime binary version does not match versions.env' >&2
  exit 1
fi

ldd_output="$(docker run --rm --entrypoint ldd "$IMAGE" /usr/local/sbin/openvpn)"
if grep -Fq 'not found' <<<"$ldd_output"; then
  printf '%s\n' "$ldd_output" >&2
  echo 'runtime OpenVPN binary has unresolved libraries' >&2
  exit 1
fi

metadata="$(docker run --rm --entrypoint ovpn "$IMAGE" version --json)"
grep -Fq "\"version\":\"$GO_RUNTIME_VERSION\"" <<<"$metadata"
grep -Fq "\"data_schema\":$DATA_SCHEMA" <<<"$metadata"
go_version="$(awk '$1 == "go" { print $2; exit }' "$ROOT_DIR/go.mod")"
grep -Fq "\"go_version\":\"go$go_version\"" <<<"$metadata"
grep -Fq '"sqlite":"github.com/mattn/go-sqlite3 v1.14.48"' <<<"$metadata"
grep -Fq '"yaml":"go.yaml.in/yaml/v3 v3.0.4"' <<<"$metadata"

short_version="$(docker run --rm --entrypoint ovpn "$IMAGE" version -s)"
if [ "$short_version" != "$GO_RUNTIME_VERSION" ]; then
  printf 'unexpected ovpn version -s output: %s\n' "$short_version" >&2
  exit 1
fi

test "$(docker run --rm --entrypoint ovpn-broker "$IMAGE" --version)" = "$GO_RUNTIME_VERSION"
test "$(docker run --rm --entrypoint ovpn-broker "$IMAGE" -v)" = "$GO_RUNTIME_VERSION"
docker run --rm --entrypoint ovpn-broker "$IMAGE" -h | grep -Fq -- '--listen|-l PATH'
set +e
broker_error="$(docker run --rm --entrypoint ovpn-broker "$IMAGE" \
  -l /tmp/same -b /tmp/same -r /tmp/raw -m 1 -B 1 -t 1s 2>&1)"
broker_code=$?
set -e
test "$broker_code" -eq 65
grep -Fq 'broker configuration is invalid' <<<"$broker_error"
test "$(docker image inspect "$IMAGE" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}')" = "$IMAGE_VERSION"
test "$(docker image inspect "$IMAGE" --format '{{ index .Config.Labels "org.opencontainers.image.licenses" }}')" = GPL-2.0-only

for binary in /usr/local/bin/ovpn /usr/local/bin/ovpn-broker; do
  ldd_output="$(docker run --rm --entrypoint ldd "$IMAGE" "$binary")"
  if grep -Fq 'not found' <<<"$ldd_output"; then
    printf '%s\n' "$ldd_output" >&2
    echo "runtime Go binary has unresolved libraries: $binary" >&2
    exit 1
  fi
done

docker run --rm --entrypoint sh "$IMAGE" -ec '
  ! command -v curl >/dev/null
  ! command -v jq >/dev/null
  command -v tar >/dev/null
  command -v openssl >/dev/null
  ! command -v python3 >/dev/null
  test "$(readlink /usr/local/bin/docker-entrypoint)" = ovpn
  test "$(readlink /usr/local/bin/ovpn-hook)" = ovpn
  test ! -e /usr/local/lib/openvpn-container/go
  test ! -e /usr/local/lib/openvpn-container
  test ! -e /usr/local/share/openvpn-container/build-info.json
  test ! -e /usr/local/share/openvpn-container/compatibility/contract.env
  test ! -e /usr/local/lib/openvpn-bootstrap.sh
  test ! -e /usr/local/lib/openvpn-verify-management-release.sh
  test ! -e /usr/local/lib/openvpn-container/upgrade.sh
  test ! -e /usr/local/share/openvpn-container/embedded-management
  test ! -e /usr/local/share/openvpn-container/trusted-management-keys
  test ! -e /usr/local/lib/openvpn-management-runtime
  test -s /usr/local/share/licenses/openvpn-container/LICENSE
  test -s /usr/local/share/licenses/openvpn-container/NOTICE
  test -s /usr/local/share/licenses/openvpn/COPYING
  test -s /usr/local/share/openvpn-container/templates/openvpn-2.7/server.conf.tpl
  test -s /usr/local/share/openvpn-container/templates/openvpn-2.7/client.ovpn.tpl
  test -s /usr/local/share/openvpn-container/compatibility/contract.json
  grep -Fq "GNU GENERAL PUBLIC LICENSE" /usr/local/share/licenses/openvpn-container/LICENSE
  grep -Fq "GPL-2.0-only" /usr/local/share/licenses/openvpn-container/NOTICE
  grep -Fq "OpenVPN" /usr/local/share/licenses/openvpn/COPYING
'
printf 'runtime image smoke passed (openvpn=%s)\n' "$OPENVPN_VERSION"
