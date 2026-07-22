#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

FILES=(
  README.md
  README_CN.md
  contracts/schema3/README.md
  contracts/schema3/README_CN.md
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

documented_environment=(
  OVPN_IMAGE
  OVPN_CONFIG_FILE
  OVPN_DATA_DIR
  OVPN_RUNTIME_DIR
  OVPN_MAINTENANCE
  OVPN_EDITOR
  EDITOR
  OVPN_BOOTSTRAP_FROM_ENV
  OVPN_BOOTSTRAP_ENDPOINT
  OVPN_BOOTSTRAP_PROTOCOL
  OVPN_BOOTSTRAP_FAMILY
  OVPN_BOOTSTRAP_PORT
  OVPN_BOOTSTRAP_CLIENT_TO_CLIENT
  OVPN_BOOTSTRAP_IPV4_NETWORK
  OVPN_BOOTSTRAP_DYNAMIC_POOL_SIZE
  OVPN_BOOTSTRAP_NAT_ENABLED
  OVPN_BOOTSTRAP_NAT_INTERFACE
  OVPN_BOOTSTRAP_REDIRECT_GATEWAY
  OVPN_BOOTSTRAP_DNS
  OVPN_BOOTSTRAP_ROUTES
  OVPN_BOOTSTRAP_LOG_MAX_BYTES
  OVPN_BOOTSTRAP_LOG_BACKUPS
  OVPN_COMPATIBILITY_FILE
  OVPN_TEMPLATE_ROOT
  OVPN_OPENVPN_BIN
  OVPN_BROKER_BIN
  OVPN_EASYRSA_BIN
  OVPN_IP_BIN
  OVPN_IPTABLES_BIN
  OVPN_IP_FORWARD_FILE
)
for variable in "${documented_environment[@]}"; do
  grep -Fq "\`$variable\`" README.md
  grep -Fq "\`$variable\`" README_CN.md
done

extract_command_headings() {
  sed -n 's/^### `\(ovpn .*\)`$/\1/p' "$1"
}

extract_command_usage() {
  local file=$1
  local label=$2
  awk -v label="$label" '
    $0 == label { waiting_for_fence = 1; next }
    waiting_for_fence && $0 == "```text" { reading_usage = 1; waiting_for_fence = 0; next }
    reading_usage { print; reading_usage = 0 }
  ' "$file"
}

diff -u \
  <(extract_command_headings docs/en/v4/commands.md) \
  <(extract_command_headings docs/cn/v4/commands.md)
diff -u \
  <(extract_command_usage docs/en/v4/commands.md 'Syntax:') \
  <(extract_command_usage docs/cn/v4/commands.md '语法：')
test "$(extract_command_headings docs/en/v4/commands.md | wc -l)" -eq \
  "$(extract_command_usage docs/en/v4/commands.md 'Syntax:' | wc -l)"

for readme in README.md README_CN.md; do
  test "$(sed -n '/^```yaml$/,/^```$/p' "$readme" | rg -c 'OVPN_BOOTSTRAP_')" -eq 3
  grep -Fq 'cp config.example.yaml openvpn-config/config.yaml' "$readme"
  grep -Fq '[.env.example](.env.example)' "$readme"
done

if rg -n \
  'ovpn (start|init|network |client ip |runtime version)|--no-trunc|--release-ip([^v]|$)|compatibility/contract\.env|meta/client-ip\.csv|meta/audit\.jsonl|/etc/openvpn-config' \
  README.md README_CN.md docs/en/v4 docs/cn/v4; then
  echo 'current documentation contains retired schema 3 interfaces' >&2
  exit 1
fi

printf 'documentation checks passed\n'
