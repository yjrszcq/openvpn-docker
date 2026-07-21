#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORKFLOWS="$ROOT_DIR/.github/workflows"

for workflow in test.yml candidate.yml upstream-check.yml release.yml; do
  test -s "$WORKFLOWS/$workflow"
done
test ! -e "$WORKFLOWS/management-release.yml"
test ! -e "$ROOT_DIR/tests/smoke/container/upgrade-state-smoke.sh"

grep -Fq 'workflow_call:' "$WORKFLOWS/test.yml"
grep -Fq 'mvdan/shfmt:v3.10.0' "$WORKFLOWS/test.yml"
grep -Fq 'rhysd/actionlint:1.7.7' "$WORKFLOWS/test.yml"
grep -Fq 'koalaman/shellcheck:v0.10.0' "$WORKFLOWS/test.yml"
grep -Fq 'actions/setup-go@v6' "$WORKFLOWS/test.yml"
grep -Fq 'go-version: 1.26.5' "$WORKFLOWS/test.yml"
grep -Fq 'GOPROXY: direct' "$WORKFLOWS/test.yml"
grep -Fq 'gofmt -l cmd internal' "$WORKFLOWS/test.yml"
grep -Fq 'go vet ./...' "$WORKFLOWS/test.yml"
grep -Fq 'go test -race ./...' "$WORKFLOWS/test.yml"
grep -Fq 'scripts/verify-go-licenses.sh' "$WORKFLOWS/test.yml"
grep -Fq 'tests/smoke/container/runtime-image-smoke.sh' "$WORKFLOWS/test.yml"
grep -Fq 'tests/smoke/container/client-lifecycle-container-smoke.sh' "$WORKFLOWS/test.yml"
grep -Fq 'tests/smoke/container/e2e-container-smoke.sh' "$WORKFLOWS/test.yml"
grep -Fq 'OVPN_LIFECYCLE_REQUIRED=1' "$WORKFLOWS/test.yml"
grep -Fq 'OVPN_E2E_REQUIRED=1' "$WORKFLOWS/test.yml"
grep -Fq 'tests/smoke/container/go-sqlite-handoff-smoke.sh' "$WORKFLOWS/test.yml"
grep -Fq 'Real schema 3 migration and rollback' "$WORKFLOWS/test.yml"
grep -Fq 'fetch-depth: 0' "$WORKFLOWS/test.yml"
grep -Fq 'linux/amd64,linux/arm64' "$WORKFLOWS/test.yml"
# shellcheck disable=SC2016 # Assert literal workflow build-argument expressions.
grep -Fq -- '--build-arg "GO_BUILD_IMAGE=$GO_BUILD_IMAGE"' "$WORKFLOWS/test.yml"
# shellcheck disable=SC2016 # Assert literal workflow build-argument expressions.
grep -Fq -- '--build-arg "GO_RUNTIME_VERSION=$GO_RUNTIME_VERSION"' "$WORKFLOWS/test.yml"
grep -Fq 'tests/smoke/shell/license-smoke.sh' "$WORKFLOWS/test.yml"
for retired in \
  management-broker-smoke.sh \
  management-hook-smoke.sh \
  schema2-uuid-migration-smoke.sh \
  schema-migration-container-smoke.sh \
  image-handoff-smoke.sh; do
  if grep -Fq "$retired" "$WORKFLOWS/test.yml"; then
    echo "implementation-coupled test remains in CI: $retired" >&2
    exit 1
  fi
done
grep -Fq 'candidate-ovpn' "$WORKFLOWS/candidate.yml"
grep -Fq 'packages: write' "$WORKFLOWS/candidate.yml"
grep -Fq "GHCR_TOKEN: \${{ github.token }}" "$WORKFLOWS/candidate.yml"
grep -Fq 'scripts/release-policy.sh' "$WORKFLOWS/candidate.yml"
grep -Fq 'image_required=false' "$WORKFLOWS/candidate.yml"
if grep -Eq 'MANAGEMENT_VERSION|PLATFORM_API|MANAGEMENT_SIGNING' \
  "$WORKFLOWS/test.yml" "$WORKFLOWS/candidate.yml"; then
  echo 'image workflows still contain online management release metadata' >&2
  exit 1
fi
if grep -Eq 'upgrade-state|OVPN_UPGRADE_' "$WORKFLOWS/test.yml"; then
  echo 'test workflow still contains updater-oriented state handoff interfaces' >&2
  exit 1
fi
grep -Fq 'schedule:' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'scripts/update-openvpn.sh' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'OPENVPN_CANDIDATE_RANGE' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'in_range=true' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'gh pr create' "$WORKFLOWS/upstream-check.yml"
grep -Fq 'workflow_run:' "$WORKFLOWS/release.yml"
grep -Fq 'name: Image Release' "$WORKFLOWS/release.yml"
grep -Fq 'image_required == '\''true'\''' "$WORKFLOWS/release.yml"
grep -Fq 'stable-cross-branch' "$WORKFLOWS/release.yml"
grep -Fq 'docker buildx imagetools create' "$WORKFLOWS/release.yml"
grep -Fq 'IMAGE_VERSION_BLOCKED' "$WORKFLOWS/release.yml"
grep -Fq 'DOCKERHUB_USERNAME: szcq' "$WORKFLOWS/release.yml"
grep -Fq 'DOCKERHUB_IMAGE: openvpn' "$WORKFLOWS/release.yml"
grep -Fq 'secrets.DOCKER_TOKEN' "$WORKFLOWS/release.yml"
grep -Fq "GHCR_TOKEN: \${{ github.token }}" "$WORKFLOWS/release.yml"
# shellcheck disable=SC2016 # This asserts the literal shell assignment in the workflow.
grep -Fq 'target_image="$DOCKERHUB_USERNAME/$DOCKERHUB_IMAGE:$OPENVPN_VERSION"' "$WORKFLOWS/release.yml"
printf 'workflow smoke passed\n'
