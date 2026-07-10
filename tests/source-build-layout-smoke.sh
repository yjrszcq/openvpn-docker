#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dockerfile="$ROOT_DIR/Dockerfile"

grep -Fq 'FROM ${BASE_IMAGE} AS builder' "$dockerfile"
grep -Fq 'COPY --from=builder /out/ /' "$dockerfile"
grep -Fq 'make DESTDIR=/out install' "$dockerfile"
grep -Fq 'fetch-openvpn-source /tmp/source' "$dockerfile"
grep -Fq 'OVPN_RUNTIME_STRATEGY=source-build' "$dockerfile"
grep -Fq 'OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION"' "$dockerfile"
grep -Fq 'grep -Fq "OpenVPN $OPENVPN_VERSION" /tmp/openvpn-version' "$dockerfile"
grep -Fq "! grep -Fq 'not found' /tmp/openvpn-ldd" "$dockerfile"

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

printf 'source build layout smoke passed\n'
