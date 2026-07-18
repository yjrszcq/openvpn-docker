#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VALIDATOR="$ROOT_DIR/scripts/validate-management-matrix.sh"
REGISTRY="$ROOT_DIR/compatibility/data-schema-releases.tsv"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

"$VALIDATOR" >"$TMP_DIR/valid.out"
grep -Fqx 'management compatibility matrix passed' "$TMP_DIR/valid.out"

cp "$REGISTRY" "$TMP_DIR/invalid-platform.tsv"
sed -i '2s/legacy-image\t-\t-/legacy-image\t1\t1/' "$TMP_DIR/invalid-platform.tsv"
if "$VALIDATOR" --registry "$TMP_DIR/invalid-platform.tsv" \
  >"$TMP_DIR/platform.out" 2>"$TMP_DIR/platform.err"; then
  echo 'legacy release unexpectedly claimed online platform compatibility' >&2
  exit 1
fi
grep -Fq 'must not claim online platform compatibility' "$TMP_DIR/platform.err"

cp "$REGISTRY" "$TMP_DIR/invalid-range.tsv"
sed -i '2s/2\.7\.0\t2\.8\.0/2.8.0\t2.7.0/' "$TMP_DIR/invalid-range.tsv"
if "$VALIDATOR" --registry "$TMP_DIR/invalid-range.tsv" \
  >"$TMP_DIR/range.out" 2>"$TMP_DIR/range.err"; then
  echo 'empty OpenVPN range unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'empty OpenVPN range' "$TMP_DIR/range.err"

head -n 4 "$REGISTRY" >"$TMP_DIR/release.tsv"
release_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"
printf '2.1.1\t%s\t3\tsigned-bundle\t2\t2\t2.7.0\t2.8.0\n' \
  "$release_commit" >>"$TMP_DIR/release.tsv"
"$VALIDATOR" --registry "$TMP_DIR/release.tsv" \
  --release-tag v2.1.1 --release-commit "$release_commit" \
  >"$TMP_DIR/release.out"
grep -Fqx 'management compatibility matrix passed' "$TMP_DIR/release.out"

cp "$TMP_DIR/release.tsv" "$TMP_DIR/incompatible-release.tsv"
sed -i '$s/signed-bundle\t2\t2/signed-bundle\t3\t3/' "$TMP_DIR/incompatible-release.tsv"
if "$VALIDATOR" --registry "$TMP_DIR/incompatible-release.tsv" \
  --release-tag v2.1.1 --release-commit "$release_commit" \
  >"$TMP_DIR/incompatible-release.out" 2>"$TMP_DIR/incompatible-release.err"; then
  echo 'release incompatible with its image platform unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'does not support its build image platform API' "$TMP_DIR/incompatible-release.err"

if "$VALIDATOR" --release-tag v9.9.9 \
  --release-commit 0123456789abcdef0123456789abcdef01234567 \
  >"$TMP_DIR/unregistered.out" 2>"$TMP_DIR/unregistered.err"; then
  echo 'unregistered management release unexpectedly passed' >&2
  exit 1
fi
grep -Eq 'release tag differs from MANAGEMENT_VERSION|missing from the release registry' \
  "$TMP_DIR/unregistered.err"

printf 'management matrix smoke passed\n'
