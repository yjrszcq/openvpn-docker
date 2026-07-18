#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TARGET_IMAGE="${OVPN_RELEASED_MIGRATION_IMAGE:-szcq/openvpn-server:released-migration}"
REQUIRED="${OVPN_RELEASED_MIGRATION_REQUIRED:-0}"
SKIP_TARGET_BUILD="${OVPN_RELEASED_MIGRATION_SKIP_TARGET_BUILD:-0}"
BUILD_NETWORK="${OVPN_RELEASED_MIGRATION_BUILD_NETWORK:-default}"
MANIFEST="$ROOT_DIR/compatibility/data-schema-releases.tsv"
WORK_DIR=''

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'released migration matrix failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'released migration matrix skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$TARGET_IMAGE" \
      -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'
command -v git >/dev/null 2>&1 || skip_or_fail 'missing command: git'
command -v jq >/dev/null 2>&1 || skip_or_fail 'missing command: jq'
[ -r "$MANIFEST" ] || skip_or_fail 'release schema manifest is missing'
if [ "$SKIP_TARGET_BUILD" != 1 ]; then
  OVPN_BUILD_NETWORK="$BUILD_NETWORK" "$ROOT_DIR/scripts/docker-build.sh" \
    -t "$TARGET_IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$TARGET_IMAGE" >/dev/null 2>&1; then
  skip_or_fail "target image not found: $TARGET_IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-released-migration.XXXXXX")"

build_release_image() {
  local version="$1" commit="$2"
  local source="$WORK_DIR/source-$version"
  local image="openvpn-released-migration-source:$version"
  local build_date

  git -C "$ROOT_DIR" cat-file -e "$commit^{commit}" ||
    skip_or_fail "release commit is unavailable: $commit"
  build_date="$(git -C "$ROOT_DIR" show -s --format=%cI "$commit")"
  mkdir -p "$source"
  git -C "$ROOT_DIR" archive "$commit" | tar -x -C "$source"
  BUILD_DATE="$build_date" OVPN_BUILD_NETWORK="$BUILD_NETWORK" "$source/scripts/docker-build.sh" \
    -t "$image" "$source" >/dev/null
  printf '%s\n' "$image"
}

run_source() {
  local image="$1" data_dir="$2"
  shift 2
  docker run --rm \
    -e OVPN_ENDPOINT=migration.example.test \
    -e OVPN_NAT=false \
    -v "$data_dir:/etc/openvpn" \
    "$image" "$@"
}

generate_release_data() {
  local version="$1" schema="$2" image="$3" data_dir="$4"

  mkdir -p "$data_dir"
  run_source "$image" "$data_dir" ovpn init >/dev/null
  if [ "$schema" = 1 ]; then
    run_source "$image" "$data_dir" ovpn add-client alpha >/dev/null
    run_source "$image" "$data_dir" ovpn add-client beta >/dev/null
    run_source "$image" "$data_dir" ovpn revoke-client beta >/dev/null
  else
    run_source "$image" "$data_dir" ovpn client create alpha --ip 10.8.0.2 >/dev/null
    run_source "$image" "$data_dir" ovpn client create beta --dynamic >/dev/null
    run_source "$image" "$data_dir" ovpn client create reusable --dynamic >/dev/null
    run_source "$image" "$data_dir" ovpn client revoke beta >/dev/null
    run_source "$image" "$data_dir" ovpn client delete reusable >/dev/null
  fi
  docker run --rm -v "$data_dir:/etc/openvpn" --entrypoint /bin/sh "$image" -ec \
    "mkdir -p /etc/openvpn/repair &&
      cp /etc/openvpn/pki/issued/alpha.crt /etc/openvpn/repair/old-alpha-$version.crt"
}

verify_migrated_data() {
  local version="$1" schema="$2" data_dir="$3"

  docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/bash \
    "$TARGET_IMAGE" -ec '
      set -euo pipefail
      version="$1"
      source_schema="$2"
      grep -Fqx 3 /etc/openvpn/config/schema-version
      grep -Fqx OVPN_CONFIG_VERSION=3 /etc/openvpn/config/project.env
      alpha_id="$(awk -F, '"'"'$2 == "alpha" && $3 == "active" { print $1 }'"'"' /etc/openvpn/meta/client-state.csv)"
      beta_id="$(awk -F, '"'"'$2 == "beta" && $3 == "revoked" { print $1 }'"'"' /etc/openvpn/meta/client-state.csv)"
      uuid_v4_regex="^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
      [[ "$alpha_id" =~ $uuid_v4_regex ]]
      [[ "$beta_id" =~ $uuid_v4_regex ]]
      grep -Fqx "# ovpn-client-id: $alpha_id" /etc/openvpn/clients/active/alpha.ovpn
      grep -Fqx "# ovpn-client-id: $beta_id" /etc/openvpn/clients/revoked/beta.ovpn
      openssl x509 -in "/etc/openvpn/pki/issued/$alpha_id.crt" -noout -subject |
        grep -Eq "CN ?= ?$alpha_id"
      openssl crl -in /etc/openvpn/pki/crl.pem -noout
      if [ "$source_schema" -lt 3 ]; then
        if openssl verify -crl_check -CAfile /etc/openvpn/pki/ca.crt \
          -CRLfile /etc/openvpn/pki/crl.pem "/etc/openvpn/repair/old-alpha-$version.crt"; then
          printf "old alpha certificate remains valid for %s\n" "$version" >&2
          exit 1
        fi
      else
        openssl verify -crl_check -CAfile /etc/openvpn/pki/ca.crt \
          -CRLfile /etc/openvpn/pki/crl.pem "/etc/openvpn/repair/old-alpha-$version.crt"
      fi
      if [ "$source_schema" -ge 2 ]; then
        grep -Fqx "$alpha_id,alpha,10.8.0.2" /etc/openvpn/data/client-ip.csv
        grep -Eq "^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12},reusable,deleted$" \
          /etc/openvpn/meta/client-state.csv
      fi
    ' -- "$version" "$schema"
  [ "$(docker run --rm -v "$data_dir:/etc/openvpn:ro" "$TARGET_IMAGE" state show)" = HEALTHY ]
}

while IFS=$'\t' read -r version commit schema _distribution _platform_min _platform_max _openvpn_min _openvpn_max; do
  [ -n "$version" ] || continue
  [[ "$version" == \#* ]] && continue
  image="$(build_release_image "$version" "$commit")"
  data_dir="$WORK_DIR/data-$version"
  generate_release_data "$version" "$schema" "$image" "$data_dir"

  if [ "$schema" -lt 3 ]; then
    set +e
    docker run --rm -v "$data_dir:/etc/openvpn:ro" "$TARGET_IMAGE" state show \
      >"$WORK_DIR/pre-$version.out" 2>"$WORK_DIR/pre-$version.err"
    status=$?
    set -e
    [ "$status" -eq 78 ]
    grep -Fq 'data schema migration required' "$WORK_DIR/pre-$version.err"
  else
    [ "$(docker run --rm -v "$data_dir:/etc/openvpn:ro" "$TARGET_IMAGE" state show)" = HEALTHY ]
  fi

  docker run --rm -e OVPN_MAINTENANCE=true -v "$data_dir:/etc/openvpn" \
    "$TARGET_IMAGE" migrate plan --json >"$WORK_DIR/plan-$version.json"
  jq -e --argjson source "$schema" \
    '.source_schema == $source and .target_schema == 3 and .blocked == false' \
    "$WORK_DIR/plan-$version.json" >/dev/null
  docker run --rm -e OVPN_MAINTENANCE=true -v "$data_dir:/etc/openvpn" \
    "$TARGET_IMAGE" migrate apply --yes >"$WORK_DIR/apply-$version.out"
  verify_migrated_data "$version" "$schema" "$data_dir"
  printf 'released migration passed: %s (%s -> 3)\n' "$version" "$schema"
done <"$MANIFEST"

printf 'released migration matrix passed\n'
