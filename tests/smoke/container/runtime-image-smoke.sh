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

metadata="$(docker run --rm --entrypoint ovpn "$IMAGE" runtime version)"
grep -Fq "\"image_version\": \"$IMAGE_VERSION\"" <<<"$metadata"
grep -Fq "\"data_schema\": $DATA_SCHEMA" <<<"$metadata"
grep -Fq '"runtime_strategy": "source-build"' <<<"$metadata"
grep -Fq "\"openvpn_version\": \"$OPENVPN_VERSION\"" <<<"$metadata"
grep -Fq "\"openvpn_source_version\": \"$OPENVPN_VERSION\"" <<<"$metadata"
if grep -Eq 'management_version|management_source|platform_api' <<<"$metadata"; then
  echo 'runtime metadata contains removed management version fields' >&2
  exit 1
fi

docker run --rm --entrypoint sh "$IMAGE" -ec '
  ! command -v curl >/dev/null
  command -v jq >/dev/null
  command -v tar >/dev/null
  command -v openssl >/dev/null
  command -v python3 >/dev/null
  python3 -m py_compile /usr/local/lib/openvpn-container/management-broker.py
  python3 -m py_compile /usr/local/lib/openvpn-container/runtime-logs.py
  python3 -m py_compile /usr/local/lib/openvpn-container/runtime-events.py
  test ! -e /usr/local/lib/openvpn-bootstrap.sh
  test ! -e /usr/local/lib/openvpn-verify-management-release.sh
  test ! -e /usr/local/lib/openvpn-container/upgrade.sh
  test ! -e /usr/local/share/openvpn-container/embedded-management
  test ! -e /usr/local/share/openvpn-container/trusted-management-keys
  test ! -e /usr/local/lib/openvpn-management-runtime
  test -s /usr/local/share/licenses/openvpn-container/LICENSE
  test -s /usr/local/share/licenses/openvpn-container/NOTICE
  test -s /usr/local/share/licenses/openvpn/COPYING
  grep -Fq "GNU GENERAL PUBLIC LICENSE" /usr/local/share/licenses/openvpn-container/LICENSE
  grep -Fq "GPL-2.0-only" /usr/local/share/licenses/openvpn-container/NOTICE
  grep -Fq "OpenVPN" /usr/local/share/licenses/openvpn/COPYING
'
printf 'runtime image smoke passed (openvpn=%s)\n' "$OPENVPN_VERSION"
