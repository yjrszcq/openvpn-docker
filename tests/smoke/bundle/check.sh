#!/usr/bin/env bash
# Native CI acceptance gate. No emulation setup and no optional skipped suites.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
architecture="${1:?amd64 or arm64 required}"
case "$architecture" in amd64 | arm64) ;; *) exit 64 ;; esac
# shellcheck source=../../../versions.env
. "$ROOT_DIR/versions.env"
archive="$ROOT_DIR/temp/dist/openvpn-server-$IMAGE_VERSION-linux-$architecture.tar.gz"
matrix="$ROOT_DIR/packaging/bundle/test-matrix.json"
if [ "$architecture" = arm64 ]; then
  "$ROOT_DIR/tests/smoke/bundle/prepare-arch-arm.sh"
  arch_image="$(jq -er .arch_arm64.image "$matrix")"
else arch_image="$(jq -er .arch_amd64.image "$matrix")"; fi
mapfile -t images < <(jq -er '.distributions[].image' "$matrix")
images+=("$arch_image")
for image in "${images[@]}"; do
  "$ROOT_DIR/tests/smoke/bundle/container-smoke.sh" "$archive" "$image" "linux/$architecture"
  "$ROOT_DIR/tests/smoke/bundle/vm-smoke.sh" "$archive" "$image"
done
"$ROOT_DIR/tests/smoke/bundle/container-smoke.sh" "$archive" scratch "linux/$architecture"
DOCKER_DEFAULT_PLATFORM="linux/$architecture" "$ROOT_DIR/tests/smoke/bundle/negative-runtime.sh" "ovpn-bundle-test:scratch-linux-$architecture"
