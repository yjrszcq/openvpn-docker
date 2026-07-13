#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo 'usage: release-policy.sh --previous-version VERSION --target-version VERSION --supported-range RANGE --image-version VERSION [--github-output PATH]' >&2
  exit 64
}

previous_version=''
target_version=''
supported_range=''
image_version=''
github_output=''
while [ "$#" -gt 0 ]; do
  case "$1" in
  --previous-version)
    shift
    [ "$#" -gt 0 ] || usage
    previous_version="$1"
    ;;
  --target-version)
    shift
    [ "$#" -gt 0 ] || usage
    target_version="$1"
    ;;
  --supported-range)
    shift
    [ "$#" -gt 0 ] || usage
    supported_range="$1"
    ;;
  --image-version)
    shift
    [ "$#" -gt 0 ] || usage
    image_version="$1"
    ;;
  --github-output)
    shift
    [ "$#" -gt 0 ] || usage
    github_output="$1"
    ;;
  *) usage ;;
  esac
  shift
done

validate_runtime_version() {
  local label="$1"
  local version="$2"

  if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "$label must use numeric major.minor.patch form" >&2
    exit 64
  fi
}

version_compare() {
  local left="$1"
  local right="$2"
  local left_major left_minor left_patch right_major right_minor right_patch
  local left_part right_part
  local -a left_parts right_parts

  IFS=. read -r left_major left_minor left_patch <<<"$left"
  IFS=. read -r right_major right_minor right_patch <<<"$right"
  left_parts=("$left_major" "$left_minor" "$left_patch")
  right_parts=("$right_major" "$right_minor" "$right_patch")
  for index in 0 1 2; do
    left_part="${left_parts[index]}"
    right_part="${right_parts[index]}"
    if ((10#$left_part < 10#$right_part)); then
      printf '%s\n' -1
      return 0
    fi
    if ((10#$left_part > 10#$right_part)); then
      printf '%s\n' 1
      return 0
    fi
  done
  printf '%s\n' 0
}

version_branch() {
  local major minor _

  IFS=. read -r major minor _ <<<"$1"
  printf '%s.%s\n' "$major" "$minor"
}

range_contains_target() {
  local clause operator bound comparison
  local -a clauses

  RANGE_VALID=true
  IFS=' ' read -ra clauses <<<"$supported_range"
  [ "${#clauses[@]}" -gt 0 ] || {
    RANGE_VALID=false
    return 1
  }
  for clause in "${clauses[@]}"; do
    if ! [[ "$clause" =~ ^(\>=|\>|\<=|\<|=)([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
      RANGE_VALID=false
      return 1
    fi
    operator="${BASH_REMATCH[1]}"
    bound="${BASH_REMATCH[2]}"
    comparison="$(version_compare "$target_version" "$bound")"
    case "$operator" in
    '>=') [ "$comparison" -ge 0 ] || return 1 ;;
    '>') [ "$comparison" -gt 0 ] || return 1 ;;
    '<=') [ "$comparison" -le 0 ] || return 1 ;;
    '<') [ "$comparison" -lt 0 ] || return 1 ;;
    '=') [ "$comparison" -eq 0 ] || return 1 ;;
    esac
  done
}

emit() {
  local key="$1"
  local value="$2"

  printf '%s=%s\n' "$key" "$value"
  if [ -n "$github_output" ]; then
    printf '%s=%s\n' "$key" "$value" >>"$github_output"
  fi
}

[ -n "$previous_version" ] || usage
[ -n "$target_version" ] || usage
[ -n "$supported_range" ] || usage
[ -n "$image_version" ] || usage
validate_runtime_version 'previous version' "$previous_version"
validate_runtime_version 'target version' "$target_version"
if ! [[ "$image_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo 'image version must use SemVer or SemVer prerelease form' >&2
  exit 64
fi

previous_branch="$(version_branch "$previous_version")"
target_branch="$(version_branch "$target_version")"
same_branch=false
if [ "$previous_branch" = "$target_branch" ]; then
  same_branch=true
fi
if range_contains_target; then
  in_range=true
else
  [ "$RANGE_VALID" = true ] || {
    echo 'supported range must contain space-separated comparison clauses' >&2
    exit 64
  }
  in_range=false
fi
release_eligible=false
if [[ "$image_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  release_eligible=true
fi

if [ "$in_range" = false ]; then
  decision=RANGE_BLOCKED
elif [ "$release_eligible" = false ]; then
  decision=IMAGE_VERSION_BLOCKED
elif [ "$same_branch" = true ]; then
  decision=READY_SAME_BRANCH
else
  decision=WAITING_CROSS_BRANCH_APPROVAL
fi

emit previous_version "$previous_version"
emit target_version "$target_version"
emit previous_branch "$previous_branch"
emit target_branch "$target_branch"
emit same_branch "$same_branch"
emit in_range "$in_range"
emit release_eligible "$release_eligible"
emit decision "$decision"
