#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

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

non_executable="$(git -C "$ROOT_DIR" ls-files --stage '*.sh' | awk '$1 != "100755" { print $4 }')"
if [ -n "$non_executable" ]; then
  printf 'tracked shell scripts must use Git mode 100755:\n%s\n' "$non_executable" >&2
  exit 1
fi

printf 'basic checks passed (%s shell files)\n' "$checked"
