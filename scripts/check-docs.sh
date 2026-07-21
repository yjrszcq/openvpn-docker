#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

FILES=(
  README.md
  README_CN.md
  docs/en/v4/commands.md
  docs/en/v4/operations.md
  docs/cn/v4/commands.md
  docs/cn/v4/operations.md
  docs/en/data-schema-upgrade-policy.md
  docs/en/image-update-policy.md
  docs/cn/data-schema-upgrade-policy.md
  docs/cn/image-update-policy.md
)

for file in "${FILES[@]}"; do
  test -s "$file"
  base="$(dirname "$file")"
  while IFS= read -r target; do
    case "$target" in
    http://* | https://* | mailto:* | \#*) continue ;;
    esac
    target="${target%%#*}"
    [ -z "$target" ] || test -e "$base/$target"
  done < <(perl -ne 'while (/\]\(([^)]+)\)/g) { print "$1\n" }' "$file")
done

if rg -n \
  'ovpn (start|init|network |client ip |runtime version)|--no-trunc|--release-ip([^v]|$)|compatibility/contract\.env|meta/client-ip\.csv|meta/audit\.jsonl|config export --output /etc/openvpn-config/config\.yaml' \
  README.md README_CN.md docs/en/v4 docs/cn/v4; then
  echo 'current documentation contains retired schema 3 interfaces' >&2
  exit 1
fi

printf 'documentation checks passed\n'
