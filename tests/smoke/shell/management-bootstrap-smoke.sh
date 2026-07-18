#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
BOOTSTRAP="$ROOT_DIR/rootfs/usr/local/lib/openvpn-bootstrap.sh"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

set -a
# shellcheck source=../../../versions.env
. "$ROOT_DIR/versions.env"
set +a

embedded="$TMP_DIR/embedded"
runtime="$TMP_DIR/runtime"
data="$TMP_DIR/data"
mkdir -p "$embedded"
ln -s "$LIB_DIR" "$embedded/lib"
ln -s "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates" "$embedded/templates"
ln -s "$ROOT_DIR/compatibility" "$embedded/compatibility"
printf 'MANAGEMENT_VERSION=%s\nPLATFORM_API=%s\nDATA_SCHEMA=%s\n' \
  "$MANAGEMENT_VERSION" "$PLATFORM_API" "$DATA_SCHEMA" >"$embedded/management.env"

build_info="$TMP_DIR/build-info.json"
OVPN_RUNTIME_STRATEGY=source-build \
OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
OVPN_VCS_REF=test-revision \
OVPN_BUILD_DATE=1970-01-01T00:00:00Z \
  "$ROOT_DIR/scripts/generate-build-info.sh" "$build_info"

run_bootstrapped() {
  env -u OVPN_LIB_DIR \
    OVPN_BOOTSTRAP_LIB="$BOOTSTRAP" \
    OVPN_EMBEDDED_MANAGEMENT_ROOT="$embedded" \
    OVPN_RUNTIME_MANAGEMENT_ROOT="$runtime" \
    OVPN_DATA_DIR="$data" \
    OVPN_BUILD_INFO="$build_info" \
    "$OVPN" "$@"
}

[ "$(run_bootstrapped -v)" = "$MANAGEMENT_VERSION" ]
test -x "$runtime/current/lib/cli.sh"
grep -Fqx 'Usage: ovpn <command> [args]' <(run_bootstrapped help)
test ! -e "$data/repair/.scripts"

run_bootstrapped help >/dev/null &
first_pid=$!
run_bootstrapped help >/dev/null &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
test -f "$runtime/releases/embedded-$MANAGEMENT_VERSION/.ready"

printf 'management bootstrap smoke passed\n'
