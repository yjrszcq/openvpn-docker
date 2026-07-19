#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT_DIR"

cat <<'EOF' | sha256sum -c -
5c5c95878150c9f297d1d3520df89b30aa85253714b9f8b7279a17e9bf566e74  docs/en/v1/commands.md
a761f1f56c5eea22b66496ada40fa3710f553f619b5c77e33fcbadbd0ed44e7d  docs/en/v1/operations.md
0bca0c77221794b6590697dded6c956b9cbc566e7775b2177ac8cbc56fbb5e5d  docs/cn/v1/commands.md
f28e39e8f83d5f772743a7c6ce514c2e1b357bb649829e165dba1b8c2c54ee36  docs/cn/v1/operations.md
d61f0134655bbe22b06e90dc395c7c0b7b8b9279edc7d0de530c201a8f18fb90  docs/en/v2/commands.md
ad314ea7f5dfed4bce0a008fa57f969f0b256bfae35420a9bef58534f8ef8c2b  docs/en/v2/operations.md
c3383632d8045831eb74311434db933ed60535e460e9f57b0808de0cee0853f1  docs/cn/v2/commands.md
9c5b4d2d27c7306c2a2d4dac391670797d00dc1f04026ee467c61c3c5e6e6562  docs/cn/v2/operations.md
EOF

for commands in \
  "$ROOT_DIR/docs/en/v3/commands.md" \
  "$ROOT_DIR/docs/cn/v3/commands.md"; do
  grep -Fq 'ovpn migrate plan [--json]' "$commands"
  grep -Fq 'ovpn migrate apply [--yes]' "$commands"
  grep -Fq 'ovpn runtime logs [--lines N] [--follow] [--raw]' "$commands"
  grep -Fq 'ovpn runtime events [--lines N] [--follow] [--json]' "$commands"
  grep -Fq 'OVPN_LOG_MAX_BYTES' "$commands"
  grep -Fq 'OVPN_LOG_BACKUPS' "$commands"
  if grep -Eq 'ovpn upgrade|--to-version|MANAGEMENT_VERSION|PLATFORM_API|repair/\.scripts' "$commands"; then
    echo "current command reference contains removed online-update interfaces: $commands" >&2
    exit 1
  fi
done
# shellcheck disable=SC2016 # Assert literal Markdown code spans.
grep -Fq 'data schema version `3`' "$ROOT_DIR/docs/en/v3/commands.md"
grep -Fq '# OpenVPN CLI v3 Reference' "$ROOT_DIR/docs/en/v3/commands.md"
# shellcheck disable=SC2016 # Assert literal Markdown code spans.
grep -Fq '数据 schema 版本 `3`' "$ROOT_DIR/docs/cn/v3/commands.md"
grep -Fq '# OpenVPN CLI v3 参考手册' "$ROOT_DIR/docs/cn/v3/commands.md"

for operations in \
  "$ROOT_DIR/docs/en/v3/operations.md" \
  "$ROOT_DIR/docs/cn/v3/operations.md"; do
  grep -Fq 'docker compose stop openvpn' "$operations"
  grep -Fq 'openvpn-maintenance migrate plan' "$operations"
  grep -Fq 'openvpn-maintenance migrate apply --yes' "$operations"
  grep -Fq 'openvpn-maintenance state doctor' "$operations"
  grep -Fq 'docker compose up -d openvpn' "$operations"
  grep -Fq 'repair/migrations/' "$operations"
  grep -Fq 'runtime logs --lines 100' "$operations"
  grep -Fq 'runtime events --lines 100 --json' "$operations"
  grep -Fq 'image-update-policy.md' "$operations"
  if grep -Eq 'ovpn upgrade|--to-version|OVPN_GITHUB_TOKEN|PLATFORM_API|repair/\.scripts' "$operations"; then
    echo "current operations guide contains removed online-update interfaces: $operations" >&2
    exit 1
  fi
done

for readme in "$ROOT_DIR/README.md" "$ROOT_DIR/README_CN.md"; do
  grep -Fq 'OVPN_LOG_MAX_BYTES' "$readme"
  grep -Fq 'OVPN_LOG_BACKUPS' "$readme"
  grep -Fq 'openvpn-maintenance migrate plan' "$readme"
  grep -Fq 'runtime logs --lines 100' "$readme"
  grep -Fq 'runtime events --lines 100 --json' "$readme"
  grep -Fq 'image-update-policy.md' "$readme"
  if grep -Eq 'ovpn upgrade|OVPN_GITHUB_TOKEN|MANAGEMENT_VERSION|PLATFORM_API|signed-bundle|data-schema-releases' "$readme"; then
    echo "README contains removed online-update interfaces: $readme" >&2
    exit 1
  fi
done

test -r "$ROOT_DIR/docs/en/image-update-policy.md"
test -r "$ROOT_DIR/docs/cn/image-update-policy.md"
test ! -e "$ROOT_DIR/docs/en/management-update-policy.md"
test ! -e "$ROOT_DIR/docs/cn/management-update-policy.md"

grep -Fq 'docs/en/v3/commands.md' "$ROOT_DIR/README.md"
grep -Fq 'docs/en/v3/operations.md' "$ROOT_DIR/README.md"
grep -Fq 'docs/cn/v3/commands.md' "$ROOT_DIR/README_CN.md"
grep -Fq 'docs/cn/v3/operations.md' "$ROOT_DIR/README_CN.md"

printf 'current documentation smoke passed\n'
