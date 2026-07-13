#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOWS="$ROOT_DIR/.github/workflows"

for workflow in test.yml candidate.yml upstream-check.yml release.yml; do
  test -s "$WORKFLOWS/$workflow"
done

grep -Fq 'workflow_call:' "$WORKFLOWS/test.yml"
grep -Fq 'mvdan/shfmt:v3.10.0' "$WORKFLOWS/test.yml"
grep -Fq 'rhysd/actionlint:1.7.7' "$WORKFLOWS/test.yml"
grep -Fq 'koalaman/shellcheck:v0.10.0' "$WORKFLOWS/test.yml"
grep -Fq 'OVPN_E2E_REQUIRED=1' "$WORKFLOWS/test.yml"
grep -Fq 'linux/amd64,linux/arm64' "$WORKFLOWS/test.yml"
grep -Fq 'tests/upgrade-state-smoke.sh' "$WORKFLOWS/test.yml"
grep -Fq 'candidate-ovpn' "$WORKFLOWS/candidate.yml"
grep -Fq 'packages: write' "$WORKFLOWS/candidate.yml"
grep -Fq 'scripts/release-policy.sh' "$WORKFLOWS/candidate.yml"
grep -Fq 'schedule:' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'scripts/update-openvpn.sh' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'gh pr create' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'workflow_run:' "$WORKFLOWS/release.yml"
grep -Fq 'stable-cross-branch' "$WORKFLOWS/release.yml"
grep -Fq 'docker buildx imagetools create' "$WORKFLOWS/release.yml"
grep -Fq 'IMAGE_VERSION_BLOCKED' "$WORKFLOWS/release.yml"

printf 'workflow smoke passed\n'
