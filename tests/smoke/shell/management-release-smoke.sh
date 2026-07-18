#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PACKAGER="$ROOT_DIR/scripts/package-management-release.sh"
VERIFIER="$ROOT_DIR/scripts/verify-management-release.sh"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

openssl genpkey -algorithm ED25519 -out "$TMP_DIR/signing-key.pem" >/dev/null 2>&1
openssl pkey -in "$TMP_DIR/signing-key.pem" -pubout -out "$TMP_DIR/signing-key.pub" >/dev/null 2>&1

build_release() {
  local output="$1"
  SOURCE_DATE_EPOCH=0 "$PACKAGER" \
    --output-dir "$output" \
    --signing-key "$TMP_DIR/signing-key.pem" \
    --vcs-ref 0123456789abcdef0123456789abcdef01234567 >/dev/null
}

build_release "$TMP_DIR/first"
build_release "$TMP_DIR/second"

for asset in management-bundle.tar.gz management-release.env management-release.env.sig; do
  cmp "$TMP_DIR/first/$asset" "$TMP_DIR/second/$asset"
done

"$VERIFIER" --release-dir "$TMP_DIR/first" --public-key "$TMP_DIR/signing-key.pub" >/dev/null
grep -Fqx 'FORMAT_VERSION=1' "$TMP_DIR/first/management-release.env"
grep -Fqx 'MANAGEMENT_VERSION=2.1.1' "$TMP_DIR/first/management-release.env"
grep -Fqx 'DATA_SCHEMA=3' "$TMP_DIR/first/management-release.env"
grep -Fqx 'PLATFORM_API_MIN=2' "$TMP_DIR/first/management-release.env"
grep -Fqx 'PLATFORM_API_MAX=2' "$TMP_DIR/first/management-release.env"
grep -Fqx 'OPENVPN_MIN=2.7.0' "$TMP_DIR/first/management-release.env"
grep -Fqx 'OPENVPN_MAX_EXCLUSIVE=2.8.0' "$TMP_DIR/first/management-release.env"
grep -Fqx 'REQUIRED_FEATURES=tls-crypt,data-ciphers,crl-verify,topology-subnet' "$TMP_DIR/first/management-release.env"

tar -tzf "$TMP_DIR/first/management-bundle.tar.gz" >"$TMP_DIR/files"
grep -Fqx './management.env' "$TMP_DIR/files"
grep -Fqx './lib/cli.sh' "$TMP_DIR/files"
grep -Fqx './lib/migrations/1-to-2.sh' "$TMP_DIR/files"
grep -Fqx './templates/openvpn-2.7/server.conf.tpl' "$TMP_DIR/files"
grep -Fqx './compatibility/contract.env' "$TMP_DIR/files"

cp -a "$TMP_DIR/first" "$TMP_DIR/tampered-manifest"
sed -i 's/DATA_SCHEMA=3/DATA_SCHEMA=4/' "$TMP_DIR/tampered-manifest/management-release.env"
if "$VERIFIER" --release-dir "$TMP_DIR/tampered-manifest" --public-key "$TMP_DIR/signing-key.pub" >/dev/null 2>&1; then
  printf 'tampered manifest was accepted\n' >&2
  exit 1
fi

cp -a "$TMP_DIR/first" "$TMP_DIR/tampered-bundle"
printf x >>"$TMP_DIR/tampered-bundle/management-bundle.tar.gz"
if "$VERIFIER" --release-dir "$TMP_DIR/tampered-bundle" --public-key "$TMP_DIR/signing-key.pub" >/dev/null 2>&1; then
  printf 'tampered bundle was accepted\n' >&2
  exit 1
fi

cp -a "$TMP_DIR/first" "$TMP_DIR/mismatched-contract"
mkdir -p "$TMP_DIR/unpacked"
tar -xzf "$TMP_DIR/mismatched-contract/management-bundle.tar.gz" -C "$TMP_DIR/unpacked"
sed -i 's/DATA_SCHEMA=3/DATA_SCHEMA=4/' "$TMP_DIR/unpacked/management.env"
tar --sort=name --format=ustar --mtime='@0' --owner=0 --group=0 --numeric-owner \
  -C "$TMP_DIR/unpacked" -cf - . | gzip -n -9 >"$TMP_DIR/mismatched-contract/management-bundle.tar.gz"
new_sha="$(sha256sum "$TMP_DIR/mismatched-contract/management-bundle.tar.gz" | awk '{print $1}')"
sed -i "s/^ASSET_SHA256=.*/ASSET_SHA256=$new_sha/" "$TMP_DIR/mismatched-contract/management-release.env"
openssl pkeyutl -sign -rawin -inkey "$TMP_DIR/signing-key.pem" \
  -in "$TMP_DIR/mismatched-contract/management-release.env" \
  -out "$TMP_DIR/mismatched-contract/management-release.env.sig"
if "$VERIFIER" --release-dir "$TMP_DIR/mismatched-contract" --public-key "$TMP_DIR/signing-key.pub" >/dev/null 2>&1; then
  printf 'mismatched bundle contract was accepted\n' >&2
  exit 1
fi

openssl genpkey -algorithm ED25519 -out "$TMP_DIR/other-key.pem" >/dev/null 2>&1
openssl pkey -in "$TMP_DIR/other-key.pem" -pubout -out "$TMP_DIR/other-key.pub" >/dev/null 2>&1
if "$VERIFIER" --release-dir "$TMP_DIR/first" --public-key "$TMP_DIR/other-key.pub" >/dev/null 2>&1; then
  printf 'unknown signing key was accepted\n' >&2
  exit 1
fi

printf 'management release smoke passed\n'
