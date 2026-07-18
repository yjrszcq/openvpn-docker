#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT_DIR"

cat <<'EOF' | sha256sum -c -
5c5c95878150c9f297d1d3520df89b30aa85253714b9f8b7279a17e9bf566e74  docs/en/v1/commands.md
a761f1f56c5eea22b66496ada40fa3710f553f619b5c77e33fcbadbd0ed44e7d  docs/en/v1/operations.md
0bca0c77221794b6590697dded6c956b9cbc566e7775b2177ac8cbc56fbb5e5d  docs/cn/v1/commands.md
f28e39e8f83d5f772743a7c6ce514c2e1b357bb649829e165dba1b8c2c54ee36  docs/cn/v1/operations.md
EOF

for commands in \
  "$ROOT_DIR/docs/en/v2/commands.md" \
  "$ROOT_DIR/docs/cn/v2/commands.md"; do
  grep -Fq 'ovpn migrate plan [--to-version VERSION] [--json]' "$commands"
  grep -Fq 'ovpn migrate apply [--to-version VERSION] [--yes]' "$commands"
  grep -Fq 'ovpn runtime logs [--lines N] [--follow] [--raw]' "$commands"
  grep -Fq 'ovpn runtime events [--lines N] [--follow] [--json]' "$commands"
  grep -Fq 'OVPN_LOG_MAX_BYTES' "$commands"
  grep -Fq 'OVPN_LOG_BACKUPS' "$commands"
done
# shellcheck disable=SC2016 # Assert literal Markdown code spans.
grep -Fq 'data schema version `3`' "$ROOT_DIR/docs/en/v2/commands.md"
# shellcheck disable=SC2016 # Assert literal Markdown code spans.
grep -Fq '数据 schema 版本 `3`' "$ROOT_DIR/docs/cn/v2/commands.md"

for operations in \
  "$ROOT_DIR/docs/en/v2/operations.md" \
  "$ROOT_DIR/docs/cn/v2/operations.md"; do
  grep -Fq 'docker compose stop openvpn' "$operations"
  grep -Fq 'openvpn-maintenance migrate plan' "$operations"
  grep -Fq 'openvpn-maintenance migrate apply --yes' "$operations"
  grep -Fq 'openvpn-maintenance state doctor' "$operations"
  grep -Fq 'docker compose up -d openvpn' "$operations"
  grep -Fq 'repair/.scripts' "$operations"
  grep -Fq 'repair/migrations/' "$operations"
  grep -Fq 'runtime logs --lines 100' "$operations"
  grep -Fq 'runtime events --lines 100 --json' "$operations"
done

for readme in "$ROOT_DIR/README.md" "$ROOT_DIR/README_CN.md"; do
  grep -Fq 'OVPN_LOG_MAX_BYTES' "$readme"
  grep -Fq 'OVPN_LOG_BACKUPS' "$readme"
  grep -Fq 'ovpn upgrade --check' "$readme"
  grep -Fq 'openvpn-maintenance migrate plan' "$readme"
  grep -Fq 'runtime logs --lines 100' "$readme"
  grep -Fq 'runtime events --lines 100 --json' "$readme"
  grep -Fq 'signed-bundle' "$readme"
done

printf 'current documentation smoke passed\n'
