#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$TMP_DIR/bin" "$TMP_DIR/keyring" "$TMP_DIR/fixtures" "$TMP_DIR/data" "$TMP_DIR/runtime"
openssl genpkey -algorithm ED25519 -out "$TMP_DIR/key.pem" >/dev/null 2>&1
openssl pkey -in "$TMP_DIR/key.pem" -pubout -out "$TMP_DIR/keyring/release.pem" >/dev/null 2>&1

cat >"$TMP_DIR/bin/openvpn" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  --version) printf 'OpenVPN 2.7.5 test-build\n' ;;
  --help)
    printf '%s\n' '--tls-crypt key' '--data-ciphers list' '--crl-verify crl' "--topology t: 'net30', 'p2p', or 'subnet'"
    exit 1
    ;;
  *) exit 64 ;;
esac
EOF

cat >"$TMP_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "${FAKE_CURL_FAIL:-0}" != 1 ] || exit 69
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    -H|--connect-timeout|--max-time) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  mock://*/releases\?*) source_file="$UPGRADE_FIXTURES/releases.json" ;;
  file://*) source_file="${url#file://}" ;;
  *) exit 69 ;;
esac
printf '%s\n' "$url" >>"$CURL_LOG"
cp "$source_file" "$output"
EOF
chmod +x "$TMP_DIR/bin/openvpn" "$TMP_DIR/bin/curl"

create_release() {
  local version="$1" schema="$2" minimum="$3" maximum="$4"
  local release="$TMP_DIR/fixtures/$version" bundle="$TMP_DIR/bundle-$version"
  local sha
  mkdir -p "$release" "$bundle/lib" "$bundle/templates" "$bundle/compatibility"
  cp -a "$LIB_DIR/." "$bundle/lib/"
  cp -a "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates/." "$bundle/templates/"
  cp -a "$ROOT_DIR/compatibility/." "$bundle/compatibility/"
  cat >"$bundle/management.env" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=$version
VCS_REF=0123456789abcdef0123456789abcdef01234567
DATA_SCHEMA=$schema
PLATFORM_API_MIN=1
PLATFORM_API_MAX=1
OPENVPN_MIN=$minimum
OPENVPN_MAX_EXCLUSIVE=$maximum
REQUIRED_FEATURES=tls-crypt,data-ciphers,crl-verify,topology-subnet
EOF
  find "$bundle" -type d -exec chmod 0755 {} +
  find "$bundle" -type f -exec chmod 0644 {} +
  find "$bundle/lib" -type f -name '*.sh' -exec chmod 0755 {} +
  tar --sort=name --format=ustar --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$bundle" -cf - . | gzip -n -9 >"$release/management-bundle.tar.gz"
  sha="$(sha256sum "$release/management-bundle.tar.gz" | awk '{print $1}')"
  cat >"$release/management-release.env" <<EOF
FORMAT_VERSION=1
MANAGEMENT_VERSION=$version
VCS_REF=0123456789abcdef0123456789abcdef01234567
DATA_SCHEMA=$schema
PLATFORM_API_MIN=1
PLATFORM_API_MAX=1
OPENVPN_MIN=$minimum
OPENVPN_MAX_EXCLUSIVE=$maximum
REQUIRED_FEATURES=tls-crypt,data-ciphers,crl-verify,topology-subnet
ASSET_NAME=management-bundle.tar.gz
ASSET_SHA256=$sha
EOF
  openssl pkeyutl -sign -rawin -inkey "$TMP_DIR/key.pem" \
    -in "$release/management-release.env" -out "$release/management-release.env.sig"
}

create_release 2.1.2 2 2.7.0 2.8.0
create_release 2.1.3 3 2.7.0 2.8.0
create_release 2.1.4 2 2.8.0 2.9.0

jq -n --arg root "file://$TMP_DIR/fixtures" '[
  {tag_name:"v2.1.3",draft:false,prerelease:false,assets:[
    {name:"management-release.env",browser_download_url:($root+"/2.1.3/management-release.env")},
    {name:"management-release.env.sig",browser_download_url:($root+"/2.1.3/management-release.env.sig")},
    {name:"management-bundle.tar.gz",browser_download_url:($root+"/2.1.3/management-bundle.tar.gz")}]},
  {tag_name:"v2.1.4",draft:false,prerelease:false,assets:[
    {name:"management-release.env",browser_download_url:($root+"/2.1.4/management-release.env")},
    {name:"management-release.env.sig",browser_download_url:($root+"/2.1.4/management-release.env.sig")},
    {name:"management-bundle.tar.gz",browser_download_url:($root+"/2.1.4/management-bundle.tar.gz")}]},
  {tag_name:"v2.1.2",draft:false,prerelease:false,assets:[
    {name:"management-release.env",browser_download_url:($root+"/2.1.2/management-release.env")},
    {name:"management-release.env.sig",browser_download_url:($root+"/2.1.2/management-release.env.sig")},
    {name:"management-bundle.tar.gz",browser_download_url:($root+"/2.1.2/management-bundle.tar.gz")}]},
  {tag_name:"v9.0.0-rc1",draft:false,prerelease:true,assets:[]}
]' >"$TMP_DIR/fixtures/releases.json"

embedded="$TMP_DIR/embedded"
mkdir -p "$embedded"
ln -s "$LIB_DIR" "$embedded/lib"
ln -s "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates" "$embedded/templates"
ln -s "$ROOT_DIR/compatibility" "$embedded/compatibility"
printf 'MANAGEMENT_VERSION=2.1.1\nPLATFORM_API=1\nDATA_SCHEMA=2\n' >"$embedded/management.env"

set -a
. "$ROOT_DIR/versions.env"
set +a
build_info="$TMP_DIR/build-info.json"
OVPN_RUNTIME_STRATEGY=source-build OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
  OVPN_VCS_REF=test OVPN_BUILD_DATE=1970-01-01T00:00:00Z \
  "$ROOT_DIR/scripts/generate-build-info.sh" "$build_info"

export OVPN_LIB_DIR="$LIB_DIR"
export OVPN_DATA_DIR="$TMP_DIR/data"
export OVPN_BUILD_INFO="$build_info"
export OVPN_OPENVPN_BIN="$TMP_DIR/bin/openvpn"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_CURL_BIN="$TMP_DIR/bin/curl"
export UPGRADE_FIXTURES="$TMP_DIR/fixtures"
export CURL_LOG="$TMP_DIR/curl.log"
export OVPN_GITHUB_API_URL=mock://repository
export OVPN_MANAGEMENT_KEYRING="$TMP_DIR/keyring"
export OVPN_MANAGEMENT_VERIFIER="$ROOT_DIR/rootfs/usr/local/lib/openvpn-verify-management-release.sh"
export OVPN_BOOTSTRAP_LIB="$ROOT_DIR/rootfs/usr/local/lib/openvpn-bootstrap.sh"
export OVPN_EMBEDDED_MANAGEMENT_ROOT="$embedded"
export OVPN_RUNTIME_MANAGEMENT_ROOT="$TMP_DIR/runtime"
mkdir -p "$OVPN_DATA_DIR/config"
printf 'OVPN_CONFIG_VERSION=2\n' >"$OVPN_DATA_DIR/config/project.env"
printf '2\n' >"$OVPN_DATA_DIR/config/schema-version"
mkdir -p "$OVPN_DATA_DIR/pki/private" "$OVPN_DATA_DIR/clients/active" "$OVPN_DATA_DIR/server"
printf 'credential sentinel\n' >"$OVPN_DATA_DIR/pki/private/client.key"
printf 'profile sentinel\n' >"$OVPN_DATA_DIR/clients/active/client.ovpn"
printf 'server sentinel\n' >"$OVPN_DATA_DIR/server/server.conf"

business_state_checksum() {
  find "$OVPN_DATA_DIR" -path "$OVPN_DATA_DIR/repair/.scripts" -prune -o -type f -print0 |
    sort -z | xargs -0 sha256sum
}
business_checksum="$(business_state_checksum)"

"$OVPN" upgrade --check --json >"$TMP_DIR/check.json"
jq -e '.current_version == "2.1.1" and .target_version == "2.1.2" and
  .platform_api == 1 and .openvpn_version == "2.7.5" and
  .current_schema == 2 and .target_schema == 2 and .schema_change == false and
  .download_asset == "management-bundle.tar.gz" and (.skipped | length) == 2' \
  "$TMP_DIR/check.json" >/dev/null
if grep -Fq 'management-bundle.tar.gz' "$CURL_LOG"; then
  printf 'upgrade --check downloaded a full bundle\n' >&2
  exit 1
fi
test ! -e "$TMP_DIR/data/repair/.scripts"

"$OVPN" upgrade --version 2.1.1 --yes >"$TMP_DIR/noop.out"
grep -Fq 'already active' "$TMP_DIR/noop.out"

set +e
"$OVPN" upgrade --version 2.1.3 --check >"$TMP_DIR/incompatible.out" 2>"$TMP_DIR/incompatible.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'requires ovpn migrate' "$TMP_DIR/incompatible.err"

set +e
FAKE_CURL_FAIL=1 "$OVPN" upgrade --check >"$TMP_DIR/network.out" 2>"$TMP_DIR/network.err"
status=$?
set -e
[ "$status" -eq 69 ]

mkdir -p "$TMP_DIR/unknown-keyring"
openssl genpkey -algorithm ED25519 -out "$TMP_DIR/unknown.pem" >/dev/null 2>&1
openssl pkey -in "$TMP_DIR/unknown.pem" -pubout -out "$TMP_DIR/unknown-keyring/release.pem" >/dev/null 2>&1
set +e
OVPN_MANAGEMENT_KEYRING="$TMP_DIR/unknown-keyring" "$OVPN" upgrade --version 2.1.2 --check \
  >"$TMP_DIR/unknown.out" 2>"$TMP_DIR/unknown.err"
status=$?
set -e
[ "$status" -eq 74 ]
grep -Fq 'signature or manifest verification failed' "$TMP_DIR/unknown.err"

mkdir -p "$TMP_DIR/data/repair/.scripts/releases/1.9.0"
"$OVPN" upgrade --yes >"$TMP_DIR/applied.out"
grep -Fqx '2.1.2' "$TMP_DIR/data/repair/.scripts/active"
grep -Fqx 'embedded' "$TMP_DIR/data/repair/.scripts/previous"
test -f "$TMP_DIR/data/repair/.scripts/releases/2.1.2/management-release.env.sig"
test ! -e "$TMP_DIR/data/repair/.scripts/releases/1.9.0"
[ "$(business_state_checksum)" = "$business_checksum" ]
case "$(readlink "$TMP_DIR/runtime/current")" in
*'/online-2.1.2-'*) ;;
*)
  printf 'upgrade did not activate hydrated online code\n' >&2
  exit 1
  ;;
esac

# A stale concurrent CLI that selected the same target must not overwrite the
# retained embedded rollback pointer.
"$OVPN" upgrade --yes >"$TMP_DIR/repeated.out"
grep -Fqx embedded "$TMP_DIR/data/repair/.scripts/previous"

run_bootstrapped() {
  env -u OVPN_LIB_DIR \
    OVPN_BOOTSTRAP_LIB="$OVPN_BOOTSTRAP_LIB" \
    OVPN_EMBEDDED_MANAGEMENT_ROOT="$embedded" \
    OVPN_RUNTIME_MANAGEMENT_ROOT="$TMP_DIR/runtime" \
    OVPN_MANAGEMENT_KEYRING="$OVPN_MANAGEMENT_KEYRING" \
    OVPN_MANAGEMENT_VERIFIER="$OVPN_MANAGEMENT_VERIFIER" \
    OVPN_DATA_DIR="$OVPN_DATA_DIR" OVPN_BUILD_INFO="$OVPN_BUILD_INFO" \
    OVPN_OPENVPN_BIN="$OVPN_OPENVPN_BIN" OVPN_CURL_BIN="$OVPN_CURL_BIN" \
    OVPN_GITHUB_API_URL="$OVPN_GITHUB_API_URL" UPGRADE_FIXTURES="$UPGRADE_FIXTURES" \
    CURL_LOG="$CURL_LOG" \
    "$OVPN" "$@"
}

[ "$(run_bootstrapped -v)" = 2.1.2 ]
run_bootstrapped upgrade --rollback --yes >"$TMP_DIR/rollback.out"
grep -Fqx embedded "$TMP_DIR/data/repair/.scripts/active"
grep -Fqx 2.1.2 "$TMP_DIR/data/repair/.scripts/previous"
grep -Fq 'rolled back to embedded' "$TMP_DIR/rollback.out"
[ "$(business_state_checksum)" = "$business_checksum" ]

exec {test_lock_fd}>"$TMP_DIR/data/repair/.scripts/.management.lock"
flock -x "$test_lock_fd"
set +e
run_bootstrapped upgrade --rollback --yes >"$TMP_DIR/locked.out" 2>"$TMP_DIR/locked.err"
status=$?
set -e
[ "$status" -eq 74 ]
grep -Fq 'another management update is in progress' "$TMP_DIR/locked.err"
flock -u "$test_lock_fd"

printf 'management upgrade smoke passed\n'
