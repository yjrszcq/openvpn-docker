#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

checked=0
while IFS= read -r -d '' file; do
  bash -n "$file"
  checked=$((checked + 1))
done < <(
  find "$ROOT_DIR" -type f \
    \( -name "*.sh" -o -name "ovpn" -o -name "docker-entrypoint" \) \
    ! -path "$ROOT_DIR/.git/*" \
    ! -path "$ROOT_DIR/goal/*" \
    ! -path "$ROOT_DIR/.checkpoint-karpathy/*" \
    -print0
)

printf 'basic checks passed (%s shell files)\n' "$checked"
