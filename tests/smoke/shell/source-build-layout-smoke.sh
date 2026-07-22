#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
dockerfile="$ROOT_DIR/Dockerfile"

grep -Fq 'FROM ${BASE_IMAGE} AS builder' "$dockerfile"
grep -Fq 'FROM ${GO_BUILD_IMAGE} AS go-builder' "$dockerfile"
grep -Fq 'COPY --from=builder /out/ /' "$dockerfile"
grep -Fq 'COPY --from=go-builder /out/ /' "$dockerfile"
grep -Fq 'make DESTDIR=/out install' "$dockerfile"
grep -Fq 'fetch-openvpn-source /tmp/source' "$dockerfile"
grep -Fq 'grep -Fq "OpenVPN $OPENVPN_VERSION" /tmp/openvpn-version' "$dockerfile"
grep -Fq "! grep -Fq 'not found' /tmp/openvpn-ldd" "$dockerfile"
grep -Fq 'GOPROXY=direct' "$dockerfile"
grep -Fq 'go build -buildvcs=false -trimpath' "$dockerfile"
grep -Fq 'internal/buildinfo.Version=$GO_RUNTIME_VERSION' "$dockerfile"
grep -Fq 'test "$GO_RUNTIME_VERSION" = "$IMAGE_VERSION"' "$dockerfile"
grep -Fq 'test "$DATA_SCHEMA" = 4' "$dockerfile"
grep -Fq 'org.opencontainers.image.version="$IMAGE_VERSION"' "$dockerfile"
grep -Fq 'org.opencontainers.image.licenses="GPL-2.0-only"' "$dockerfile"
grep -Fq '/usr/local/lib/openvpn-container/go/ovpn-broker' "$dockerfile"
grep -Fq "grep -Fq 'CGO_ENABLED=1'" "$dockerfile"

for removed in embedded-management openvpn-bootstrap.sh trusted-management-keys \
  MANAGEMENT_SIGNING_PUBLIC_KEY_B64 MANAGEMENT_VERSION PLATFORM_API; do
  if grep -Fq "$removed" "$dockerfile"; then
    echo "runtime Dockerfile still contains online-update artifact: $removed" >&2
    exit 1
  fi
done

runtime_packages="$(awk 'seen { print } /^FROM \\$\\{BASE_IMAGE\\}$/ { seen = 1 }' "$dockerfile")"
if printf '%s\n' "$runtime_packages" | grep -Eq '^[[:space:]]+curl([[:space:]]|\\\\)'; then
  echo 'runtime Dockerfile must not install curl for online updates' >&2
  exit 1
fi

if ! grep -Eq '^[[:space:]]+nano([[:space:]]|\\\\)' "$dockerfile"; then
  echo 'runtime Dockerfile must install nano for interactive client editing' >&2
  exit 1
fi

if ! grep -Eq '^[[:space:]]+vim([[:space:]]|\\\\)' "$dockerfile"; then
  echo 'runtime Dockerfile must install Vim for configured interactive editing' >&2
  exit 1
fi

for removed in python3 jq socat procps util-linux generate-build-info; do
  if grep -Eq "^[[:space:]]+${removed}([[:space:]]|\\\\)|COPY .*${removed}" "$dockerfile"; then
    echo "runtime Dockerfile still includes legacy dependency: $removed" >&2
    exit 1
  fi
done
if grep -Fq 'COPY rootfs/ /' "$dockerfile"; then
  echo 'runtime Dockerfile must not copy the legacy rootfs control plane' >&2
  exit 1
fi
grep -Fq 'COPY templates/ /usr/local/share/openvpn-container/templates/' "$dockerfile"
grep -Fq 'COPY compatibility/contract.json ' "$dockerfile"
if grep -Fq 'COPY compatibility/ ' "$dockerfile"; then
  echo 'runtime Dockerfile must copy only the strict JSON compatibility contract' >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+openvpn([[:space:]]|\\\\)' "$dockerfile"; then
  echo 'runtime Dockerfile must not install the Debian openvpn package' >&2
  exit 1
fi

if ! grep -Fq 'ldd /out/usr/local/sbin/openvpn' "$dockerfile"; then
  echo 'builder must capture OpenVPN runtime libraries from ldd output' >&2
  exit 1
fi

if ! grep -Fq 'readlink -f "$library"' "$dockerfile" || ! grep -Fq 'ln -sf "$(basename "$resolved")"' "$dockerfile"; then
  echo 'builder must preserve resolved runtime libraries and their SONAME links' >&2
  exit 1
fi
if [ "$(grep -cF '|| exit 1;' "$dockerfile")" -lt 3 ]; then
  echo 'builder must propagate every runtime library copy failure' >&2
  exit 1
fi

printf 'source build layout smoke passed\n'
