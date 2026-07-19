#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HOOK="$ROOT_DIR/rootfs/usr/local/bin/ovpn-hook"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$TMP_DIR/lib"
cat >"$TMP_DIR/lib/pool-persist-hook.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s:%s\n' "${script_type:-}" "${common_name:-}" >"$HOOK_MARKER"
EOF
chmod +x "$TMP_DIR/lib/pool-persist-hook.sh"

OVPN_LIB_DIR="$TMP_DIR/lib" HOOK_MARKER="$TMP_DIR/hook.log" \
  script_type=client-connect common_name=client-id \
  "$HOOK" pool-persist
grep -Fqx 'client-connect:client-id' "$TMP_DIR/hook.log"

set +e
OVPN_LIB_DIR="$TMP_DIR/missing" "$HOOK" pool-persist \
  >"$TMP_DIR/missing.out" 2>"$TMP_DIR/missing.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'image does not provide pool-persist' "$TMP_DIR/missing.err"

set +e
"$HOOK" unknown >"$TMP_DIR/unknown.out" 2>"$TMP_DIR/unknown.err"
status=$?
set -e
[ "$status" -eq 64 ]
grep -Fq 'unknown hook' "$TMP_DIR/unknown.err"

printf 'fixed management hook smoke passed\n'
