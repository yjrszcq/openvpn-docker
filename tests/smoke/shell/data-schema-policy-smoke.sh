#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST="$ROOT_DIR/compatibility/data-schema-releases.jsonl"

test -r "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md"
test -r "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md"
test -r "$ROOT_DIR/docs/en/management-update-policy.md"
test -r "$ROOT_DIR/docs/cn/management-update-policy.md"
test -r "$MANIFEST"

"$ROOT_DIR/scripts/validate-management-matrix.sh" >/dev/null
cmp <(jq -c . "$MANIFEST") "$MANIFEST"
jq -s -e '
  length == 4 and
  ([.[].management_version] | unique | length) == 4 and
  ([.[].commit] | unique | length) == 4 and
  any(.[]; .management_version == "1.0.0" and
    .commit == "6619921e5257e604f5df2c63d2fa10505b680d84" and
    .data_schema == 1 and .distribution == "legacy-image" and
    .platform_api == null) and
  any(.[]; .management_version == "2.0.0" and
    .commit == "6f8b77dfe58087fe66073929d70d89d8c92e6cac" and
    .data_schema == 2 and .distribution == "legacy-image" and
    .platform_api == null) and
  any(.[]; .management_version == "2.1.0" and
    .commit == "a8ddaca5345a9cc75cf04b56d07b0072a9d44019" and
    .data_schema == 2 and .distribution == "legacy-image" and
    .platform_api == null) and
  any(.[]; .management_version == "2.1.1" and
    .commit == "11bdee954b2e875621f83a21564d048593adb68a" and
    .data_schema == 2 and .distribution == "legacy-image" and
    .platform_api == null) and
  all(.[].openvpn; . == {"supported": ["2.7.5"]})
' "$MANIFEST" >/dev/null

grep -Fq 'runs only its current schema' "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md"
grep -Fq '支持旧版本是指支持其迁移' "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md"
grep -Fq 'data-schema-upgrade-policy.md' "$ROOT_DIR/README.md"
grep -Fq 'data-schema-upgrade-policy.md' "$ROOT_DIR/README_CN.md"
grep -Fq 'management-update-policy.md' "$ROOT_DIR/README.md"
grep -Fq 'management-update-policy.md' "$ROOT_DIR/README_CN.md"
grep -Fq 'ovpn migrate' "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md"
grep -Fq 'ovpn migrate' "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md"
grep -Fq '/etc/openvpn/repair/.scripts' "$ROOT_DIR/docs/en/management-update-policy.md"
grep -Fq '/etc/openvpn/repair/.scripts' "$ROOT_DIR/docs/cn/management-update-policy.md"

printf 'data schema policy smoke passed\n'
