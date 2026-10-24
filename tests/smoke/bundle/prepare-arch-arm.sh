#!/usr/bin/env bash
# The pinned hash was verified against the official Arch Linux ARM signature.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
matrix="$ROOT_DIR/packaging/bundle/test-matrix.json"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
curl -fL --retry 3 "$(jq -er .arch_arm64.url "$matrix")" -o "$work/rootfs.tar.gz"
printf '%s  %s\n' "$(jq -er .arch_arm64.sha256 "$matrix")" "$work/rootfs.tar.gz" | sha256sum -c
docker import --platform linux/arm64 "$work/rootfs.tar.gz" "$(jq -er .arch_arm64.image "$matrix")"
