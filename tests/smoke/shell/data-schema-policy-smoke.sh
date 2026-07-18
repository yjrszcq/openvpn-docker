#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST="$ROOT_DIR/compatibility/data-schema-releases.tsv"

test -r "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md"
test -r "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md"
test -r "$ROOT_DIR/docs/en/management-update-policy.md"
test -r "$ROOT_DIR/docs/cn/management-update-policy.md"
test -r "$MANIFEST"

awk -F '\t' '
  NR == 1 {
    if ($0 != "# management_version\tcommit\tdata_schema") exit 1
    next
  }
  NF != 3 { exit 1 }
  $1 !~ /^[0-9]+\.[0-9]+\.[0-9]+$/ { exit 1 }
  $2 !~ /^[0-9a-f]{40}$/ { exit 1 }
  $3 !~ /^[1-9][0-9]*$/ { exit 1 }
  seen_version[$1]++ { if (seen_version[$1] != 1) exit 1 }
  seen_commit[$2]++ { if (seen_commit[$2] != 1) exit 1 }
  END { if (NR != 5) exit 1 }
' "$MANIFEST"

grep -Fqx $'1.0.0\t6619921e5257e604f5df2c63d2fa10505b680d84\t1' "$MANIFEST"
grep -Fqx $'2.0.0\t6f8b77dfe58087fe66073929d70d89d8c92e6cac\t2' "$MANIFEST"
grep -Fqx $'2.1.0\ta8ddaca5345a9cc75cf04b56d07b0072a9d44019\t2' "$MANIFEST"
grep -Fqx $'2.1.1\t11bdee954b2e875621f83a21564d048593adb68a\t2' "$MANIFEST"

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
