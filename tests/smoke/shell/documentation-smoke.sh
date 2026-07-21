#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
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

for mapping in \
  '--help.*-h' '--json.*-j' '--output.*-o' '--yes.*-y' \
  '--name.*-n' '--id.*-i' '--ipv4.*-4' '--release-ipv4.*-4' \
  '--all.*-a' '--detail.*-d' '--full-id.*-u' '--lines.*-l' \
  '--follow.*-f' '--raw.*-r' '--short.*-s'; do
  rg -q -- "$mapping" docs/en/v4/commands.md
  rg -q -- "$mapping" docs/cn/v4/commands.md
done

for mapping in \
  '--version/-v' '--listen/-l' '--backend/-b' '--raw-log/-r' \
  '--max-bytes/-m' '--backups/-B' '--timeout/-t'; do
  grep -Fq -- "$mapping" docs/en/v4/commands.md
  grep -Fq -- "$mapping" docs/cn/v4/commands.md
done

for command in \
  'server init' 'server run' 'server render' \
  'config validate' 'config show' 'config export' 'config plan' 'config apply' \
  'client create' 'client list' 'client export' 'client rename' 'client revoke' 'client reissue' 'client delete' \
  'client address set' 'client address edit' 'client address release' \
  'state show' 'state doctor' 'repair plan' 'repair apply' \
  'migrate plan' 'migrate apply' \
  'runtime status' 'runtime health' 'runtime capabilities' 'runtime logs' 'runtime events' \
  'version'; do
  grep -Fq "ovpn $command" docs/en/v4/commands.md
  grep -Fq "ovpn $command" docs/cn/v4/commands.md
done

if rg -n \
  'ovpn (start|init|network |client ip |runtime version)|--no-trunc|--release-ip([^v]|$)|compatibility/contract\.env|meta/client-ip\.csv|meta/audit\.jsonl|config export --output /etc/openvpn-config/config\.yaml' \
  README.md README_CN.md docs/en/v4 docs/cn/v4; then
  echo 'current documentation contains retired schema 3 interfaces' >&2
  exit 1
fi

printf 'v4 documentation smoke passed\n'
