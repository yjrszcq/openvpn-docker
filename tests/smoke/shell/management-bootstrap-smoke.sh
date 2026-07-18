#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
BOOTSTRAP="$ROOT_DIR/rootfs/usr/local/lib/openvpn-bootstrap.sh"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

set -a
# shellcheck source=../../../versions.env
. "$ROOT_DIR/versions.env"
set +a

embedded="$TMP_DIR/embedded"
runtime="$TMP_DIR/runtime"
data="$TMP_DIR/data"
mkdir -p "$embedded"
ln -s "$LIB_DIR" "$embedded/lib"
ln -s "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates" "$embedded/templates"
ln -s "$ROOT_DIR/compatibility" "$embedded/compatibility"
printf 'MANAGEMENT_VERSION=%s\nPLATFORM_API=%s\nDATA_SCHEMA=%s\n' \
  "$MANAGEMENT_VERSION" "$PLATFORM_API" "$DATA_SCHEMA" >"$embedded/management.env"

build_info="$TMP_DIR/build-info.json"
OVPN_RUNTIME_STRATEGY=source-build \
  OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
  OVPN_VCS_REF=test-revision \
  OVPN_BUILD_DATE=1970-01-01T00:00:00Z \
  "$ROOT_DIR/scripts/generate-build-info.sh" "$build_info"

run_bootstrapped() {
  env -u OVPN_LIB_DIR \
    OVPN_BOOTSTRAP_LIB="$BOOTSTRAP" \
    OVPN_EMBEDDED_MANAGEMENT_ROOT="$embedded" \
    OVPN_RUNTIME_MANAGEMENT_ROOT="$runtime" \
    OVPN_MANAGEMENT_KEYRING="$TMP_DIR/keyring" \
    OVPN_MANAGEMENT_VERIFIER="$ROOT_DIR/rootfs/usr/local/lib/openvpn-verify-management-release.sh" \
    OVPN_DATA_DIR="$data" \
    OVPN_BUILD_INFO="$build_info" \
    "$OVPN" "$@"
}

[ "$(run_bootstrapped -v)" = "$MANAGEMENT_VERSION" ]
test -x "$runtime/current/lib/cli.sh"
grep -Fqx 'Usage: ovpn <command> [args]' <(run_bootstrapped help)
test ! -e "$data/repair/.scripts"

run_bootstrapped help >/dev/null &
first_pid=$!
run_bootstrapped help >/dev/null &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
test -f "$runtime/releases/embedded-$MANAGEMENT_VERSION/.ready"

mkdir -p "$TMP_DIR/keyring" "$data/repair/.scripts/releases/$MANAGEMENT_VERSION"
openssl genpkey -algorithm ED25519 -out "$TMP_DIR/key.pem" >/dev/null 2>&1
openssl pkey -in "$TMP_DIR/key.pem" -pubout -out "$TMP_DIR/keyring/release.pem" >/dev/null 2>&1
SOURCE_DATE_EPOCH=0 "$ROOT_DIR/scripts/package-management-release.sh" \
  --output-dir "$data/repair/.scripts/releases/$MANAGEMENT_VERSION" \
  --signing-key "$TMP_DIR/key.pem" \
  --vcs-ref 0123456789abcdef0123456789abcdef01234567 >/dev/null
printf '%s\n' "$MANAGEMENT_VERSION" >"$data/repair/.scripts/active"
[ "$(run_bootstrapped -v)" = "$MANAGEMENT_VERSION" ]
grep -Fq '"management_source": "online"' <(run_bootstrapped runtime version)
case "$(readlink "$runtime/current")" in
*"/online-$MANAGEMENT_VERSION-"*) ;;
*)
  printf 'online bundle was not selected by bootstrap\n' >&2
  exit 1
  ;;
esac

rm -rf "$runtime"
[ "$(run_bootstrapped -v)" = "$MANAGEMENT_VERSION" ]
grep -Fq '"management_source": "online"' <(run_bootstrapped runtime version)

mkdir -p "$data/repair/.scripts/transactions"
cat >"$data/repair/.scripts/transactions/activation.env" <<EOF
STATE=prepared
OLD_ACTIVE=$MANAGEMENT_VERSION
OLD_PREVIOUS=embedded
NEW_ACTIVE=embedded
NEW_PREVIOUS=$MANAGEMENT_VERSION
EOF
printf 'embedded\n' >"$data/repair/.scripts/active"
printf '%s\n' "$MANAGEMENT_VERSION" >"$data/repair/.scripts/previous"
run_bootstrapped help >/dev/null
grep -Fqx "$MANAGEMENT_VERSION" "$data/repair/.scripts/active"
grep -Fqx embedded "$data/repair/.scripts/previous"
test ! -e "$data/repair/.scripts/transactions/activation.env"

cat >"$data/repair/.scripts/transactions/activation.env" <<EOF
STATE=committed
OLD_ACTIVE=$MANAGEMENT_VERSION
OLD_PREVIOUS=embedded
NEW_ACTIVE=embedded
NEW_PREVIOUS=$MANAGEMENT_VERSION
EOF
run_bootstrapped help >/dev/null
grep -Fqx embedded "$data/repair/.scripts/active"
grep -Fqx "$MANAGEMENT_VERSION" "$data/repair/.scripts/previous"
test ! -e "$data/repair/.scripts/transactions/activation.env"
printf '%s\n' "$MANAGEMENT_VERSION" >"$data/repair/.scripts/active"

printf 'invalid\n' >"$data/repair/.scripts/transactions/activation.env"
run_bootstrapped help >/dev/null 2>"$TMP_DIR/invalid-transaction.err"
grep -Fq 'invalid management activation transaction' "$TMP_DIR/invalid-transaction.err"
case "$(readlink "$runtime/current")" in
*"/embedded-$MANAGEMENT_VERSION") ;;
*)
  printf 'invalid activation transaction did not force embedded fallback\n' >&2
  exit 1
  ;;
esac
rm -f "$data/repair/.scripts/transactions/activation.env"

printf x >>"$data/repair/.scripts/releases/$MANAGEMENT_VERSION/management-bundle.tar.gz"
run_bootstrapped help >/dev/null 2>"$TMP_DIR/fallback.err"
grep -Fq 'using embedded fallback' "$TMP_DIR/fallback.err"
case "$(readlink "$runtime/current")" in
*"/embedded-$MANAGEMENT_VERSION") ;;
*)
  printf 'tampered active bundle did not fall back to embedded code\n' >&2
  exit 1
  ;;
esac

# Signed bundle verification is not enough: the immutable bootstrap rejects
# archive links and traversal names before extraction.
# shellcheck source=../../../rootfs/usr/local/lib/openvpn-bootstrap.sh
. "$BOOTSTRAP"
mkdir -p "$TMP_DIR/unsafe-link"
ln -s /etc/passwd "$TMP_DIR/unsafe-link/escape"
tar -czf "$TMP_DIR/unsafe-link.tar.gz" -C "$TMP_DIR/unsafe-link" .
if ovpn_bootstrap_bundle_is_safe "$TMP_DIR/unsafe-link.tar.gz"; then
  printf 'bootstrap accepted a bundle containing a symbolic link\n' >&2
  exit 1
fi
mkdir -p "$TMP_DIR/unsafe-path"
printf x >"$TMP_DIR/unsafe-path/payload"
tar -czf "$TMP_DIR/unsafe-path.tar.gz" --transform='s|payload|../payload|' \
  -C "$TMP_DIR/unsafe-path" payload 2>/dev/null
if ovpn_bootstrap_bundle_is_safe "$TMP_DIR/unsafe-path.tar.gz" 2>/dev/null; then
  printf 'bootstrap accepted a bundle containing path traversal\n' >&2
  exit 1
fi

# A new image must not keep running old-schema online code merely because that
# bundle matches the old data. The embedded current-schema CLI must take over
# and enforce the migration gate.
old_release="$data/repair/.scripts/releases/2.0.0"
old_bundle="$TMP_DIR/old-schema-bundle"
online_root="$(find "$runtime/releases" -maxdepth 1 -type d \
  -name "online-$MANAGEMENT_VERSION-*" -print -quit)"
mkdir -p "$old_release" "$old_bundle"
cp -a "$online_root/." "$old_bundle/"
rm -f "$old_bundle/.ready"
sed -i \
  -e 's/^MANAGEMENT_VERSION=.*/MANAGEMENT_VERSION=2.0.0/' \
  -e 's/^DATA_SCHEMA=.*/DATA_SCHEMA=2/' \
  "$old_bundle/management.env"
tar --sort=name --format=ustar --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -C "$old_bundle" -cf - . | gzip -n -9 >"$old_release/management-bundle.tar.gz"
old_sha="$(sha256sum "$old_release/management-bundle.tar.gz" | awk '{print $1}')"
sed \
  -e 's/^MANAGEMENT_VERSION=.*/MANAGEMENT_VERSION=2.0.0/' \
  -e 's/^DATA_SCHEMA=.*/DATA_SCHEMA=2/' \
  -e "s/^ASSET_SHA256=.*/ASSET_SHA256=$old_sha/" \
  "$data/repair/.scripts/releases/$MANAGEMENT_VERSION/management-release.env" \
  >"$old_release/management-release.env"
openssl pkeyutl -sign -rawin -inkey "$TMP_DIR/key.pem" \
  -in "$old_release/management-release.env" \
  -out "$old_release/management-release.env.sig"
printf '%s\n' 2.0.0 >"$data/repair/.scripts/active"
mkdir -p "$data/config"
printf 'OVPN_CONFIG_VERSION=2\n' >"$data/config/project.env"
printf '2\n' >"$data/config/schema-version"
rm -rf "$runtime"
[ "$(run_bootstrapped -v 2>"$TMP_DIR/old-schema-fallback.err")" = "$MANAGEMENT_VERSION" ]
grep -Fq 'using embedded fallback' "$TMP_DIR/old-schema-fallback.err"
set +e
run_bootstrapped state show \
  >"$TMP_DIR/old-schema-state.out" 2>"$TMP_DIR/old-schema-state.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'data schema migration required' "$TMP_DIR/old-schema-state.err"

printf 'management bootstrap smoke passed\n'
