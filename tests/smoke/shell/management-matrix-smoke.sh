#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VALIDATOR="$ROOT_DIR/scripts/validate-management-matrix.sh"
REGISTRY="$ROOT_DIR/compatibility/data-schema-releases.jsonl"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

"$VALIDATOR" >"$TMP_DIR/valid.out"
grep -Fqx 'management compatibility matrix passed' "$TMP_DIR/valid.out"

jq -c '
  if .management_version == "1.0.0"
  then .platform_api = {"min": 1, "max": 1}
  else .
  end
' "$REGISTRY" >"$TMP_DIR/invalid-platform.jsonl"
if "$VALIDATOR" --registry "$TMP_DIR/invalid-platform.jsonl" \
  >"$TMP_DIR/platform.out" 2>"$TMP_DIR/platform.err"; then
  echo 'legacy release unexpectedly claimed online platform compatibility' >&2
  exit 1
fi
grep -Fq 'must not claim online platform compatibility' "$TMP_DIR/platform.err"

jq -c '
  if .management_version == "1.0.0"
  then .openvpn = {"supported": ["2.7.4"]}
  else .
  end
' "$REGISTRY" >"$TMP_DIR/unverified-runtime.jsonl"
if "$VALIDATOR" --registry "$TMP_DIR/unverified-runtime.jsonl" \
  >"$TMP_DIR/unverified-runtime.out" 2>"$TMP_DIR/unverified-runtime.err"; then
  echo 'release source outside its verified OpenVPN set unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'not in its registered verified set' "$TMP_DIR/unverified-runtime.err"

jq -c '
  if .management_version == "1.0.0"
  then .openvpn = {"supported": ["2.7.5", "2.7.5"]}
  else .
  end
' "$REGISTRY" >"$TMP_DIR/duplicate-versions.jsonl"
if "$VALIDATOR" --registry "$TMP_DIR/duplicate-versions.jsonl" \
  >"$TMP_DIR/duplicate-versions.out" 2>"$TMP_DIR/duplicate-versions.err"; then
  echo 'duplicate verified OpenVPN versions unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'must be unique and increasing' "$TMP_DIR/duplicate-versions.err"

cp "$REGISTRY" "$TMP_DIR/release.jsonl"
release_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"
jq -nc --arg commit "$release_commit" '{
  management_version: "3.0.0",
  commit: $commit,
  data_schema: 3,
  distribution: "signed-bundle",
  platform_api: {"min": 2, "max": 2},
  openvpn: {"supported": ["2.7.5"]}
}' >>"$TMP_DIR/release.jsonl"
"$VALIDATOR" --registry "$TMP_DIR/release.jsonl" \
  --release-tag v3.0.0 --release-commit "$release_commit" \
  >"$TMP_DIR/release.out"
grep -Fqx 'management compatibility matrix passed' "$TMP_DIR/release.out"

jq -c '
  if .management_version == "3.0.0"
  then .platform_api = {"min": 3, "max": 3}
  else .
  end
' "$TMP_DIR/release.jsonl" >"$TMP_DIR/incompatible-release.jsonl"
if "$VALIDATOR" --registry "$TMP_DIR/incompatible-release.jsonl" \
  --release-tag v3.0.0 --release-commit "$release_commit" \
  >"$TMP_DIR/incompatible-release.out" 2>"$TMP_DIR/incompatible-release.err"; then
  echo 'release incompatible with its image platform unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'does not support its build image platform API' "$TMP_DIR/incompatible-release.err"

jq -c '
  if .management_version == "1.0.0"
  then .unexpected = true
  else .
  end
' "$REGISTRY" >"$TMP_DIR/unknown-field.jsonl"
if "$VALIDATOR" --registry "$TMP_DIR/unknown-field.jsonl" \
  >"$TMP_DIR/unknown-field.out" 2>"$TMP_DIR/unknown-field.err"; then
  echo 'release object with an unknown field unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'registry line 1 is not a valid release object' "$TMP_DIR/unknown-field.err"

jq -c '
  if .management_version == "1.0.0"
  then .data_schema = "1"
  else .
  end
' "$REGISTRY" >"$TMP_DIR/invalid-type.jsonl"
if "$VALIDATOR" --registry "$TMP_DIR/invalid-type.jsonl" \
  >"$TMP_DIR/invalid-type.out" 2>"$TMP_DIR/invalid-type.err"; then
  echo 'release object with an invalid type unexpectedly passed' >&2
  exit 1
fi
grep -Fq 'registry line 1 is not a valid release object' "$TMP_DIR/invalid-type.err"

if "$VALIDATOR" --release-tag v9.9.9 \
  --release-commit 0123456789abcdef0123456789abcdef01234567 \
  >"$TMP_DIR/unregistered.out" 2>"$TMP_DIR/unregistered.err"; then
  echo 'unregistered management release unexpectedly passed' >&2
  exit 1
fi
grep -Eq 'release tag differs from MANAGEMENT_VERSION|missing from the release registry' \
  "$TMP_DIR/unregistered.err"

printf 'management matrix smoke passed\n'
