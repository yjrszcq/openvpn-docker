#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HOOK="$ROOT_DIR/rootfs/usr/local/bin/ovpn-hook"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

embedded="$TMP_DIR/embedded"
runtime="$TMP_DIR/runtime"
store="$TMP_DIR/data/repair/.scripts"
online="$TMP_DIR/online"
mkdir -p "$embedded/lib" "$online/lib" "$store/releases/2.1.2" "$TMP_DIR/data/config"

cat >"$embedded/management.env" <<'EOF'
MANAGEMENT_VERSION=2.1.1
PLATFORM_API=1
DATA_SCHEMA=2
EOF
cat >"$embedded/lib/cli.sh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$embedded/lib/pool-persist-hook.sh" <<'EOF'
#!/usr/bin/env bash
printf 'embedded:%s:%s\n' "${script_type:-}" "${common_name:-}" >>"$HOOK_MARKER"
EOF

cat >"$online/management.env" <<'EOF'
FORMAT_VERSION=1
MANAGEMENT_VERSION=2.1.2
VCS_REF=0123456789abcdef0123456789abcdef01234567
DATA_SCHEMA=2
PLATFORM_API_MIN=1
PLATFORM_API_MAX=1
OPENVPN_SUPPORTED_VERSIONS=2.7.5
REQUIRED_FEATURES=tls-crypt,data-ciphers,crl-verify,topology-subnet
EOF
cat >"$online/lib/cli.sh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$online/lib/pool-persist-hook.sh" <<'EOF'
#!/usr/bin/env bash
printf 'online:%s:%s\n' "${script_type:-}" "${common_name:-}" >>"$HOOK_MARKER"
EOF
chmod +x "$embedded/lib/cli.sh" "$embedded/lib/pool-persist-hook.sh" \
  "$online/lib/cli.sh" "$online/lib/pool-persist-hook.sh"

tar --sort=name --format=ustar --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -C "$online" -cf - . | gzip -n -9 >"$store/releases/2.1.2/management-bundle.tar.gz"
sha="$(sha256sum "$store/releases/2.1.2/management-bundle.tar.gz" | awk '{print $1}')"
cat >"$store/releases/2.1.2/management-release.env" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=2.1.2
VCS_REF=0123456789abcdef0123456789abcdef01234567
DATA_SCHEMA=2
PLATFORM_API_MIN=1
PLATFORM_API_MAX=1
OPENVPN_SUPPORTED_VERSIONS=2.7.5
REQUIRED_FEATURES=tls-crypt,data-ciphers,crl-verify,topology-subnet
ASSET_NAME=management-bundle.tar.gz
ASSET_SHA256=$sha
EOF
: >"$store/releases/2.1.2/management-release.env.sig"
printf '2\n' >"$TMP_DIR/data/config/schema-version"

cat >"$TMP_DIR/verifier" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$TMP_DIR/verifier"

run_hook() {
  env OVPN_BOOTSTRAP_LIB="$ROOT_DIR/rootfs/usr/local/lib/openvpn-bootstrap.sh" \
    OVPN_EMBEDDED_MANAGEMENT_ROOT="$embedded" \
    OVPN_RUNTIME_MANAGEMENT_ROOT="$runtime" \
    OVPN_MANAGEMENT_STORE="$store" \
    OVPN_MANAGEMENT_VERIFIER="$TMP_DIR/verifier" \
    OVPN_DATA_DIR="$TMP_DIR/data" HOOK_MARKER="$TMP_DIR/hook.log" \
    script_type=client-connect common_name="$1" \
    "$HOOK" pool-persist
}

run_hook first
grep -Fqx 'embedded:client-connect:first' "$TMP_DIR/hook.log"

printf '2.1.2\n' >"$store/active"
run_hook second
grep -Fqx 'online:client-connect:second' "$TMP_DIR/hook.log"

printf 'embedded\n' >"$store/active"
run_hook third
grep -Fqx 'embedded:client-connect:third' "$TMP_DIR/hook.log"

set +e
"$HOOK" unknown >"$TMP_DIR/unknown.out" 2>"$TMP_DIR/unknown.err"
status=$?
set -e
[ "$status" -eq 64 ]
grep -Fq 'unknown hook' "$TMP_DIR/unknown.err"

printf 'management hook smoke passed\n'
