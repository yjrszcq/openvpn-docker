#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REGISTRY="${OVPN_RELEASE_REGISTRY:-$ROOT_DIR/compatibility/data-schema-releases.tsv}"
RELEASE_TAG=''
RELEASE_COMMIT=''

usage() {
  cat >&2 <<'EOF'
Usage: validate-management-matrix.sh [--registry FILE]
       validate-management-matrix.sh --release-tag vX.Y.Z --release-commit COMMIT [--registry FILE]

Validate the published management/schema compatibility registry and migration
chain. Release mode additionally requires the tagged source to be registered as
a signed bundle with its exact commit and current compatibility contract.
EOF
  exit 64
}

die() {
  printf 'management matrix: %s\n' "$*" >&2
  exit 1
}

semver_compare() {
  local left="$1" right="$2"
  local left_major left_minor left_patch right_major right_minor right_patch

  IFS=. read -r left_major left_minor left_patch <<<"$left"
  IFS=. read -r right_major right_minor right_patch <<<"$right"
  for pair in \
    "$left_major:$right_major" \
    "$left_minor:$right_minor" \
    "$left_patch:$right_patch"; do
    if ((10#${pair%%:*} < 10#${pair#*:})); then
      printf '%s\n' -1
      return
    fi
    if ((10#${pair%%:*} > 10#${pair#*:})); then
      printf '%s\n' 1
      return
    fi
  done
  printf '%s\n' 0
}

version_in_range() {
  local version="$1" minimum="$2" maximum="$3"

  [ "$(semver_compare "$version" "$minimum")" -ge 0 ] &&
    [ "$(semver_compare "$version" "$maximum")" -lt 0 ]
}

while [ "$#" -gt 0 ]; do
  case "$1" in
  --registry)
    [ "$#" -ge 2 ] || usage
    REGISTRY="$2"
    shift 2
    ;;
  --release-tag)
    [ "$#" -ge 2 ] || usage
    RELEASE_TAG="$2"
    shift 2
    ;;
  --release-commit)
    [ "$#" -ge 2 ] || usage
    RELEASE_COMMIT="$2"
    shift 2
    ;;
  -h | --help)
    usage
    ;;
  *)
    usage
    ;;
  esac
done

if [ -n "$RELEASE_TAG" ] || [ -n "$RELEASE_COMMIT" ]; then
  if [ -z "$RELEASE_TAG" ] || [ -z "$RELEASE_COMMIT" ]; then
    usage
  fi
fi

[ -r "$REGISTRY" ] || die 'release registry is missing'
command -v git >/dev/null 2>&1 || die 'git is required'

# shellcheck disable=SC1091
. "$ROOT_DIR/versions.env"
# shellcheck disable=SC1091
. "$ROOT_DIR/compatibility/contract.env"

[[ "$DATA_SCHEMA" =~ ^[1-9][0-9]*$ ]] || die 'current DATA_SCHEMA is invalid'
[[ "$PLATFORM_API" =~ ^[1-9][0-9]*$ ]] || die 'current PLATFORM_API is invalid'
[[ "$OPENVPN_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  die 'current OPENVPN_VERSION is invalid'
if ! [[ "$OPENVPN_SUPPORTED_MIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  ! [[ "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  [ "$(semver_compare "$OPENVPN_SUPPORTED_MIN" "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE")" -ge 0 ]; then
  die 'current OpenVPN compatibility range is invalid'
fi
version_in_range "$OPENVPN_VERSION" \
  "$OPENVPN_SUPPORTED_MIN" "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE" ||
  die 'current OpenVPN version is outside the compatibility contract'
grep -Fqx "OVPN_CURRENT_DATA_SCHEMA=$DATA_SCHEMA" \
  "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/schema.sh" ||
  die 'versions.env DATA_SCHEMA differs from the runtime schema constant'

expected_header=$'# management_version\tcommit\tdata_schema\tdistribution\tplatform_api_min\tplatform_api_max\topenvpn_min\topenvpn_max_exclusive'
IFS= read -r header <"$REGISTRY"
[ "$header" = "$expected_header" ] || die 'release registry header is invalid'

previous_version=''
previous_schema=0
release_found=false
line_number=1
declare -A seen_commits=()
while IFS=$'\t' read -r version commit schema distribution platform_min platform_max openvpn_min openvpn_max extra; do
  line_number=$((line_number + 1))
  [ -z "$extra" ] || die "registry line $line_number has extra fields"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "registry line $line_number has an invalid management version"
  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] ||
    die "registry line $line_number has an invalid commit"
  [[ "$schema" =~ ^[1-9][0-9]*$ ]] ||
    die "registry line $line_number has an invalid schema"
  [ "$schema" -ge "$previous_schema" ] ||
    die 'release registry schemas must be monotonically increasing'
  [ "$schema" -le "$DATA_SCHEMA" ] ||
    die "release $version uses schema newer than the current source"
  previous_schema="$schema"
  [ -z "${seen_commits[$commit]:-}" ] ||
    die "registered commit is reused by multiple releases: $commit"
  seen_commits["$commit"]=true
  [[ "$openvpn_min" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "registry line $line_number has an invalid OpenVPN minimum"
  [[ "$openvpn_max" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "registry line $line_number has an invalid OpenVPN maximum"
  [ "$(semver_compare "$openvpn_min" "$openvpn_max")" -lt 0 ] ||
    die "registry line $line_number has an empty OpenVPN range"
  if [ -n "$previous_version" ]; then
    [ "$(semver_compare "$previous_version" "$version")" -lt 0 ] ||
      die 'release registry versions must be unique and strictly increasing'
  fi
  previous_version="$version"

  case "$distribution" in
  legacy-image)
    if [ "$platform_min" != - ] || [ "$platform_max" != - ]; then
      die "legacy image $version must not claim online platform compatibility"
    fi
    ;;
  signed-bundle)
    if ! [[ "$platform_min" =~ ^[1-9][0-9]*$ ]] ||
      ! [[ "$platform_max" =~ ^[1-9][0-9]*$ ]] ||
      [ "$platform_min" -gt "$platform_max" ]; then
      die "signed bundle $version has an invalid platform API range"
    fi
    ;;
  *)
    die "registry line $line_number has an invalid distribution"
    ;;
  esac

  git -C "$ROOT_DIR" cat-file -e "$commit^{commit}" 2>/dev/null ||
    die "registered commit is unavailable for $version"
  historical_versions="$(git -C "$ROOT_DIR" show "$commit:versions.env")" ||
    die "registered commit $commit has no versions.env"
  if [ "$distribution" = signed-bundle ]; then
    grep -Fqx "MANAGEMENT_VERSION=$version" <<<"$historical_versions" ||
      die "registered management version $version differs from its source commit"
  else
    grep -Fqx "IMAGE_VERSION=$version" <<<"$historical_versions" ||
      die "registered image version $version differs from its source commit"
  fi
  historical_openvpn="$(sed -n 's/^OPENVPN_VERSION=//p' <<<"$historical_versions")"
  [[ "$historical_openvpn" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die "release $version source has an invalid OpenVPN version"
  version_in_range "$historical_openvpn" "$openvpn_min" "$openvpn_max" ||
    die "release $version OpenVPN version is outside its registered range"

  if [ -n "$RELEASE_TAG" ] && [ "$version" = "${RELEASE_TAG#v}" ]; then
    release_found=true
    [ "$commit" = "$RELEASE_COMMIT" ] ||
      die 'release tag is not registered at the exact source commit'
    [ "$schema" = "$DATA_SCHEMA" ] ||
      die 'release registry schema differs from versions.env'
    [ "$distribution" = signed-bundle ] ||
      die 'new management releases must use signed-bundle distribution'
    if [ "$PLATFORM_API" -lt "$platform_min" ] ||
      [ "$PLATFORM_API" -gt "$platform_max" ]; then
      die 'release bundle does not support its build image platform API'
    fi
    if [ "$openvpn_min" != "$OPENVPN_SUPPORTED_MIN" ] ||
      [ "$openvpn_max" != "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE" ]; then
      die 'release registry OpenVPN range differs from the compatibility contract'
    fi
  fi
done < <(tail -n +2 "$REGISTRY")

[ "$line_number" -gt 1 ] || die 'release registry has no releases'

for ((schema = 1; schema < DATA_SCHEMA; schema++)); do
  [ -r "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/migrations/$schema-to-$((schema + 1)).sh" ] ||
    die "migration chain is missing $schema-to-$((schema + 1))"
done

if [ -n "$RELEASE_TAG" ]; then
  [[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
    die 'release tag is not a stable management version'
  [[ "$RELEASE_COMMIT" =~ ^[0-9a-f]{40}$ ]] ||
    die 'release commit must be a full lowercase hash'
  [ "$RELEASE_TAG" = "v$MANAGEMENT_VERSION" ] ||
    die 'release tag differs from MANAGEMENT_VERSION'
  [ "$release_found" = true ] ||
    die 'management release is missing from the release registry'
fi

printf 'management compatibility matrix passed\n'
