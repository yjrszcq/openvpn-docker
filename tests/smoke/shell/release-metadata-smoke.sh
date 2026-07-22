#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
OUTPUT="$("$ROOT_DIR/scripts/verify-release-metadata.sh")"

grep -Fqx 'image_version=4.0.1' <<<"$OUTPUT"
grep -Fqx 'data_schema=4' <<<"$OUTPUT"
grep -Fqx 'go_version=1.26.5' <<<"$OUTPUT"
grep -Fqx 'sqlite=github.com/mattn/go-sqlite3 v1.14.48; license=MIT' <<<"$OUTPUT"
grep -Fqx 'yaml=go.yaml.in/yaml/v3 v3.0.4; license=MIT-and-Apache-2.0' <<<"$OUTPUT"

mkdir -p "$TMP_DIR/scripts" "$TMP_DIR/internal/buildinfo"
cp "$ROOT_DIR/scripts/verify-release-metadata.sh" "$TMP_DIR/scripts/"
cp "$ROOT_DIR/internal/buildinfo/info.go" "$TMP_DIR/internal/buildinfo/"
cp "$ROOT_DIR/versions.env" "$ROOT_DIR/go.mod" "$ROOT_DIR/LICENSE" "$ROOT_DIR/NOTICE" "$ROOT_DIR/Dockerfile" "$TMP_DIR/"
sed -i 's/^GO_RUNTIME_VERSION=${IMAGE_VERSION}$/GO_RUNTIME_VERSION=4.0.2/' "$TMP_DIR/versions.env"
set +e
"$TMP_DIR/scripts/verify-release-metadata.sh" >"$TMP_DIR/mismatch.out" 2>"$TMP_DIR/mismatch.err"
status=$?
set -e
test "$status" -eq 78
grep -Fq 'IMAGE_VERSION and GO_RUNTIME_VERSION must match' "$TMP_DIR/mismatch.err"

printf 'release metadata smoke passed\n'
