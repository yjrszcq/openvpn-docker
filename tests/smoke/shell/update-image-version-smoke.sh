#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d)"
FIXTURE="$TMP_DIR/repository"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$FIXTURE"
git -C "$ROOT_DIR" archive HEAD | tar -x -C "$FIXTURE"
tracked_files=(
  versions.env
  internal/buildinfo/info.go
  internal/cli/run_test.go
  tests/smoke/shell/release-metadata-smoke.sh
  docs/en/image-update-policy.md
  docs/cn/image-update-policy.md
)
for file in "${tracked_files[@]}"; do
  cp "$ROOT_DIR/$file" "$FIXTURE/$file"
done
cp "$ROOT_DIR/scripts/update-image-version.sh" "$FIXTURE/scripts/update-image-version.sh"
git -C "$FIXTURE" init -q
git -C "$FIXTURE" add "${tracked_files[@]}"

SCRIPT="$FIXTURE/scripts/update-image-version.sh"
"$SCRIPT" 4.0.2 >"$TMP_DIR/same.out"
grep -Fqx 'image version is already 4.0.2; metadata is consistent' "$TMP_DIR/same.out"

before="$(git -C "$FIXTURE" hash-object internal/buildinfo/info.go)"
set +e
"$SCRIPT" 4.0 >"$TMP_DIR/invalid.out" 2>"$TMP_DIR/invalid.err"
status=$?
set -e
test "$status" -eq 64
grep -Fqx 'VERSION must use numeric major.minor.patch form' "$TMP_DIR/invalid.err"
test "$before" = "$(git -C "$FIXTURE" hash-object internal/buildinfo/info.go)"

"$SCRIPT" 4.0.3 >"$TMP_DIR/update.out"
grep -Fqx 'updated project image version from 4.0.2 to 4.0.3' "$TMP_DIR/update.out"
grep -Fqx 'IMAGE_VERSION=4.0.3' "$FIXTURE/versions.env"
grep -Fq 'Version   = "4.0.3"' "$FIXTURE/internal/buildinfo/info.go"
"$FIXTURE/scripts/verify-release-metadata.sh" >/dev/null

"$SCRIPT" 4.0.2 >/dev/null
sed -i 's/Version   = "4.0.2"/Version   = "9.9.9"/' "$FIXTURE/internal/buildinfo/info.go"
set +e
"$SCRIPT" 4.0.2 >"$TMP_DIR/drift.out" 2>"$TMP_DIR/drift.err"
status=$?
set -e
test "$status" -eq 65
grep -Fq 'internal/buildinfo/info.go: expected 1 occurrence(s) of 4.0.2, found 0' "$TMP_DIR/drift.err"

sed -i 's/Version   = "9.9.9"/Version   = "4.0.2"/' "$FIXTURE/internal/buildinfo/info.go"
before_hashes="$(for file in "${tracked_files[@]}"; do git -C "$FIXTURE" hash-object "$file"; done)"
cat >"$FIXTURE/scripts/verify-release-metadata.sh" <<'FAIL_SECOND_VALIDATION'
#!/usr/bin/env bash
set -euo pipefail
counter_file="${0}.counter"
counter=0
if [ -f "$counter_file" ]; then
  counter="$(cat "$counter_file")"
fi
counter=$((counter + 1))
printf '%s\n' "$counter" >"$counter_file"
if [ "$counter" -gt 1 ]; then
  exit 78
fi
FAIL_SECOND_VALIDATION
chmod +x "$FIXTURE/scripts/verify-release-metadata.sh"
set +e
"$SCRIPT" 4.0.3 >"$TMP_DIR/rollback.out" 2>"$TMP_DIR/rollback.err"
status=$?
set -e
test "$status" -eq 65
grep -Fqx 'updated files failed release metadata validation; changes were rolled back' "$TMP_DIR/rollback.err"
after_hashes="$(for file in "${tracked_files[@]}"; do git -C "$FIXTURE" hash-object "$file"; done)"
test "$before_hashes" = "$after_hashes"

printf 'update image version smoke passed\n'
