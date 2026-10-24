#!/usr/bin/env bash
# Execute the shipped archive, without installing any project dependency.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
archive="${1:?archive required}"
base="${2:-debian:13-slim}"
platform="${3:-linux/amd64}"
image="ovpn-bundle-test:$(printf '%s' "${base%@*}-$platform" | tr '/:' '--')"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
tar -xzf "$archive" -C "$work"
mkdir -p "$work/rootfs/tmp"
chmod 1777 "$work/rootfs/tmp"
if [ "$base" = scratch ]; then
  mkdir -p "$work/rootfs/etc" "$work/rootfs/bin" "$work/rootfs/usr/bin"
  printf 'root:x:0:0:root:/root:/bin/sh\n' >"$work/rootfs/etc/passwd"
  printf 'root:x:0:\n' >"$work/rootfs/etc/group"
  # User-supplied editor fixtures use this conventional shebang. The only shell
  # installed in this otherwise empty rootfs is the shipped static runtime.
  ln -s /opt/ovpn/bin/sh "$work/rootfs/bin/sh"
  ln -s /opt/ovpn/bin/timeout "$work/rootfs/usr/bin/timeout"
fi
cat >"$work/Dockerfile" <<'DOCKERFILE'
ARG BASE=debian:13-slim
FROM ${BASE}
COPY rootfs/ /
COPY openvpn-server/ /opt/ovpn/
ENV PATH="/opt/ovpn/bin:$PATH" OVPN_DATA_DIR=/etc/openvpn OVPN_CONFIG_FILE=/etc/ovpn-conf/config.yaml OVPN_RUNTIME_DIR=/run/openvpn-container
ENTRYPOINT ["/opt/ovpn/ovpn"]
DOCKERFILE
docker build --platform "$platform" --build-arg "BASE=$base" -t "$image" "$work"
export DOCKER_DEFAULT_PLATFORM="$platform"
docker run --rm --entrypoint sh "$image" -ec '
 if [ -r /etc/os-release ]; then cat /etc/os-release; else echo "scratch root filesystem"; fi
 mkdir -p /tmp
 cd /tmp
 ovpn version --json
 openssl version
 openvpn --version
 cd /opt/ovpn
 sha256sum -c MANIFEST.sha256 >/dev/null
 ln -s /opt/ovpn/ovpn /tmp/ovpn
 /tmp/ovpn version --short
'
docker run --rm --cap-add NET_ADMIN --device /dev/net/tun \
  -v "$ROOT_DIR/tests/smoke/bundle/portability.sh:/test.sh:ro" \
  --entrypoint /opt/ovpn/runtime/bin/busybox "$image" sh /test.sh
OVPN_CLI_UX_NATIVE=1 OVPN_CLI_UX_IMAGE="$image" OVPN_CLI_UX_SKIP_BUILD=1 OVPN_CLI_UX_REQUIRED=1 "$ROOT_DIR/tests/smoke/container/cli-ux-container-smoke.sh"
OVPN_LIFECYCLE_IMAGE="$image" OVPN_LIFECYCLE_SKIP_BUILD=1 OVPN_LIFECYCLE_REQUIRED=1 "$ROOT_DIR/tests/smoke/container/client-lifecycle-container-smoke.sh"
OVPN_BACKUP_IMAGE="$image" OVPN_BACKUP_SKIP_BUILD=1 OVPN_BACKUP_REQUIRED=1 "$ROOT_DIR/tests/smoke/container/backup-restore-container-smoke.sh"
OVPN_API_IMAGE="$image" OVPN_API_SKIP_BUILD=1 OVPN_API_REQUIRED=1 "$ROOT_DIR/tests/smoke/container/api-container-smoke.sh"
OVPN_E2E_IMAGE="$image" OVPN_E2E_SKIP_BUILD=1 OVPN_E2E_REQUIRED=1 "$ROOT_DIR/tests/smoke/container/e2e-container-smoke.sh"
printf 'bundle container functional verification passed: %s %s\n' "$base" "$platform"
