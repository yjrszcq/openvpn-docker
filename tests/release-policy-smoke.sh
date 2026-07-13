#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY="$ROOT_DIR/scripts/release-policy.sh"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

assert_policy() {
  local name="$1"
  local previous="$2"
  local target="$3"
  local range="$4"
  local image="$5"
  shift 5
  local output="$TMP_DIR/$name.out"

  "$POLICY" \
    --previous-version "$previous" \
    --target-version "$target" \
    --supported-range "$range" \
    --image-version "$image" >"$output"
  while [ "$#" -gt 0 ]; do
    grep -Fqx "$1" "$output"
    shift
  done
}

assert_policy same 2.7.5 2.7.6 '>=2.7.0 <2.8.0' 1.2.3 \
  'same_branch=true' \
  'in_range=true' \
  'release_eligible=true' \
  'decision=READY_SAME_BRANCH'
assert_policy cross 2.7.6 2.8.0 '>=2.7.0 <2.9.0' 1.3.0 \
  'same_branch=false' \
  'in_range=true' \
  'release_eligible=true' \
  'decision=WAITING_CROSS_BRANCH_APPROVAL'
assert_policy blocked 2.7.6 2.8.0 '>=2.7.0 <2.8.0' 1.3.0 \
  'in_range=false' \
  'decision=RANGE_BLOCKED'
assert_policy prerelease 2.7.5 2.7.6 '>=2.7.0 <2.8.0' 1.2.3-dev \
  'release_eligible=false' \
  'decision=IMAGE_VERSION_BLOCKED'

github_output="$TMP_DIR/github-output"
"$POLICY" \
  --previous-version 2.7.5 \
  --target-version 2.7.6 \
  --supported-range '>=2.7.0 <2.8.0' \
  --image-version 1.2.3 \
  --github-output "$github_output" >/dev/null
grep -Fqx 'decision=READY_SAME_BRANCH' "$github_output"

set +e
"$POLICY" \
  --previous-version invalid \
  --target-version 2.7.6 \
  --supported-range '>=2.7.0 <2.8.0' \
  --image-version 1.2.3 >"$TMP_DIR/invalid.out" 2>"$TMP_DIR/invalid.err"
status=$?
set -e
[ "$status" -eq 64 ]
grep -Fq 'previous version must use numeric major.minor.patch form' "$TMP_DIR/invalid.err"

printf 'release policy smoke passed\n'
