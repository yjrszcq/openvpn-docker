#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  echo 'usage: update-image-version.sh VERSION' >&2
  exit 64
}

if [ "$#" -ne 1 ]; then
  usage
fi
new_version="$1"
if ! [[ "$new_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'VERSION must use numeric major.minor.patch form' >&2
  exit 64
fi

versions_file="$ROOT_DIR/versions.env"
old_version="$(sed -n 's/^IMAGE_VERSION=//p' "$versions_file")"
if ! [[ "$old_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'versions.env must define exactly one numeric IMAGE_VERSION' >&2
  exit 65
fi
files=(
  versions.env
  internal/buildinfo/info.go
  internal/cli/run_test.go
  tests/smoke/shell/release-metadata-smoke.sh
  docs/en/image-update-policy.md
  docs/cn/image-update-policy.md
)
expected_counts=(1 1 5 1 1 1)

# Refuse a partial update if the known release-version sites have drifted.
for index in "${!files[@]}"; do
  file="${files[$index]}"
  expected="${expected_counts[$index]}"
  count="$(grep -Foc "$old_version" "$ROOT_DIR/$file" || true)"
  if [ "$count" -ne "$expected" ]; then
    printf '%s: expected %s occurrence(s) of %s, found %s\n' "$file" "$expected" "$old_version" "$count" >&2
    exit 65
  fi
  git -C "$ROOT_DIR" ls-files --error-unmatch "$file" >/dev/null 2>&1 || {
    echo "refusing to update untracked file: $file" >&2
    exit 65
  }
done

"$ROOT_DIR/scripts/verify-release-metadata.sh" >/dev/null
if [ "$new_version" = "$old_version" ]; then
  printf 'image version is already %s; metadata is consistent\n' "$new_version"
  exit 0
fi

old_pattern="${old_version//./\\.}"
backup_dir="$(mktemp -d)"
rollback=1
cleanup() {
  exit_code=$?
  trap - EXIT
  if [ "$rollback" -eq 1 ]; then
    for file in "${files[@]}"; do
      if [ -f "$backup_dir/$file" ]; then
        cp -p "$backup_dir/$file" "$ROOT_DIR/$file"
      fi
    done
  fi
  rm -rf "$backup_dir"
  exit "$exit_code"
}
trap cleanup EXIT
for file in "${files[@]}"; do
  mkdir -p "$backup_dir/$(dirname "$file")"
  cp -p "$ROOT_DIR/$file" "$backup_dir/$file"
  sed -i "s/$old_pattern/$new_version/g" "$ROOT_DIR/$file"
done

if ! "$ROOT_DIR/scripts/verify-release-metadata.sh" >/dev/null; then
  echo 'updated files failed release metadata validation; changes were rolled back' >&2
  exit 65
fi
rollback=0
printf 'updated project image version from %s to %s\n' "$old_version" "$new_version"
