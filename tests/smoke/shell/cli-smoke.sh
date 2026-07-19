#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
build_info_path="$(mktemp "${TMPDIR:-/tmp}/openvpn-cli-build-info.XXXXXX")"
data_dir="$(mktemp -d "${TMPDIR:-/tmp}/openvpn-cli-data.XXXXXX")"
trap 'rm -f "$build_info_path"; rm -rf "$data_dir"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a
OVPN_RUNTIME_STRATEGY=source-build \
OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
OVPN_VCS_REF=test-revision \
OVPN_BUILD_DATE=1970-01-01T00:00:00Z \
"$ROOT_DIR/scripts/generate-build-info.sh" "$build_info_path"
export OVPN_BUILD_INFO="$build_info_path"

"$OVPN" help >/tmp/ovpn-help.out
if ! grep -q 'Usage: ovpn' /tmp/ovpn-help.out; then
  echo 'help output missing usage' >&2
  exit 1
fi


assert_help() {
  local expected="$1"
  shift
  "$OVPN" "$@" >/tmp/ovpn-help.out
  if ! grep -Fq "$expected" /tmp/ovpn-help.out; then
    echo "help output missing expected usage: $expected" >&2
    exit 1
  fi
}

assert_help "Usage: ovpn" -h
assert_help "Usage: ovpn init" init --help
assert_help "Usage: ovpn start" start -h
assert_help "Usage: ovpn config <command>" config --help
assert_help "Usage: ovpn config apply" config apply -h
assert_help "Usage: ovpn client <command> [args]" client -h
assert_help "Usage: ovpn client create <name> [--dynamic|-d|--ip|-I <IPv4>]" client create --help
assert_help "Usage: ovpn client ip <command> [args]" client ip -h
assert_help "Usage: ovpn client ip set <name...>|--id|-i <ID>|--name|-n <NAME>|--all|-a [--dynamic|-d|--ip|-I <IPv4>]" client ip set --help
assert_help "Usage: ovpn network <command> [options]" network --help
assert_help "Usage: ovpn network apply [--network|-n <CIDR>] [--dynamic-pool-size|-p <N>] [--yes|-y]" network apply -h
assert_help "Usage: ovpn repair <command>" repair --help
assert_help "Usage: ovpn repair plan [--json|-j]" repair plan -h
assert_help "Usage: ovpn state <command>" state --help
assert_help "Usage: ovpn state doctor [--json|-j]" state doctor -h
assert_help "Usage: ovpn render <target> [options]" render --help
assert_help "Usage: ovpn render client <name>|--id|-i <ID>|--name|-n <NAME> [--stdout|-s|--output|-o <path>]" render client -h
assert_help "Usage: ovpn runtime <command>" runtime --help
assert_help "Usage: ovpn runtime version" runtime version -h
assert_help "Usage: ovpn migrate <command> [options]" migrate --help
assert_help "Usage: ovpn migrate plan [--json|-j]" migrate plan -h
assert_help "Usage: ovpn migrate apply [--yes|-y]" migrate apply --help

assert_help "Usage: ovpn config show" config show --help
assert_help "Usage: ovpn client export <name>|--id|-i <ID>|--name|-n <NAME>" client export -h
assert_help "Usage: ovpn client list [--detail|-d] [--no-trunc|-t]" client list --help
assert_help "Usage: ovpn client rename <name>|--id|-i <ID>|--name|-n <NAME> <new-name>" client rename -h
assert_help "Usage: ovpn client revoke <name>|--id|-i <ID>|--name|-n <NAME> [--release-ip|-r]" client revoke -h
assert_help "Usage: ovpn client ip release <name>|--id|-i <ID>|--name|-n <NAME>" client ip release --help
assert_help "Usage: ovpn client reissue <name>|--id|-i <ID>|--name|-n <NAME> [--dynamic|-d|--ip|-I <IPv4>]" client reissue -h
assert_help "Usage: ovpn client delete <name>|--id|-i <ID>|--name|-n <NAME>" client delete --help
assert_help "Usage: ovpn network plan [--network|-n <CIDR>] [--dynamic-pool-size|-p <N>]" network plan --help
assert_help "Usage: ovpn repair apply" repair apply -h
assert_help "Usage: ovpn state show" state show --help
assert_help "Usage: ovpn render server [--stdout|-s|--output|-o <path>]" render server -h
assert_help "Usage: ovpn runtime status" runtime status --help
assert_help "Usage: ovpn runtime health" runtime health -h
assert_help "Usage: ovpn runtime capabilities" runtime capabilities --help
assert_help "usage: ovpn runtime logs [--lines|-l N] [--follow|-f] [--raw|-r] [--no-trunc|-t]" runtime logs -h
assert_help "usage: ovpn runtime events [--lines|-l N] [--follow|-f] [--json|-j] [--no-trunc|-t]" runtime events --help

export OVPN_DATA_DIR="$data_dir"

assert_rejected_usage() {
  local expected="$1"
  shift
  if "$OVPN" "$@" >/tmp/ovpn-invalid.out 2>/tmp/ovpn-invalid.err; then
    echo "invalid command arguments unexpectedly succeeded: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" /tmp/ovpn-invalid.err; then
    echo "invalid command arguments did not report expected usage: $expected" >&2
    exit 1
  fi
}

assert_rejected_usage "usage: ovpn init" init unexpected
assert_rejected_usage "usage: ovpn start" start unexpected
assert_rejected_usage "usage: ovpn config show" config show unexpected
assert_rejected_usage "usage: ovpn config apply" config apply unexpected
assert_rejected_usage "usage: ovpn -v" -v unexpected
assert_rejected_usage "usage: ovpn --version" --version unexpected

declare -A public_short_options=()
while IFS='|' read -r context long_option short_option; do
  key="$context:$short_option"
  if [ -n "${public_short_options[$key]:-}" ]; then
    echo "duplicate public short option in '$context': $short_option" >&2
    exit 1
  fi
  public_short_options["$key"]="$long_option"
  if [ "$context" = ovpn ]; then
    help_output="$("$OVPN" -h)"
  else
    read -r -a command_parts <<<"$context"
    help_output="$("$OVPN" "${command_parts[@]}" -h)"
  fi
  grep -Fq -- "$long_option" <<<"$help_output"
  grep -Fq -- "$short_option" <<<"$help_output"
done <<'EOF'
ovpn|--help|-h
ovpn|--version|-V
client export|--id|-i
client export|--name|-n
client ip set|--all|-a
client list|--detail|-d
client create|--dynamic|-d
network apply|--dynamic-pool-size|-p
runtime logs|--follow|-f
client create|--ip|-I
repair plan|--json|-j
runtime logs|--lines|-l
network apply|--network|-n
client list|--no-trunc|-t
render server|--output|-o
runtime logs|--raw|-r
client revoke|--release-ip|-r
render server|--stdout|-s
network apply|--yes|-y
EOF

network_help="$("$OVPN" network --help)"
grep -Fq -- '-n, --network CIDR' <<<"$network_help"
grep -Fq -- '-p, --dynamic-pool-size N' <<<"$network_help"
grep -Fq -- '-y, --yes' <<<"$network_help"

"$OVPN" runtime version >/tmp/ovpn-version.out
if ! grep -Fq "\"image_version\": \"$IMAGE_VERSION\"" /tmp/ovpn-version.out; then
  echo 'version output missing image_version' >&2
  exit 1
fi
if [ "$("$OVPN" -v)" != "$IMAGE_VERSION" ]; then
  echo 'short version output does not report image version' >&2
  exit 1
fi
if grep -Eq 'management_version|management_source|platform_api' /tmp/ovpn-version.out; then
  echo 'version output contains removed management version fields' >&2
  exit 1
fi
if ! grep -Fq "\"openvpn_source_version\": \"$OPENVPN_VERSION\"" /tmp/ovpn-version.out; then
  echo 'version output missing openvpn_source_version' >&2
  exit 1
fi
"$OVPN" --version >/tmp/ovpn-version-summary.out
"$OVPN" -V >/tmp/ovpn-version-short-alias.out
cmp /tmp/ovpn-version-summary.out /tmp/ovpn-version-short-alias.out
grep -Fqx "image:           $IMAGE_VERSION" /tmp/ovpn-version-summary.out
grep -Fqx "openvpn:         $OPENVPN_VERSION" /tmp/ovpn-version-summary.out
grep -Eq '^easy-rsa:        (unknown|[0-9]+\.[0-9]+\.[0-9]+)$' /tmp/ovpn-version-summary.out
grep -Fqx "data schema:     $DATA_SCHEMA" /tmp/ovpn-version-summary.out
[ "$(wc -l </tmp/ovpn-version-summary.out)" -eq 4 ]
[ "$(tail -n 1 /tmp/ovpn-version-summary.out)" = "data schema:     $DATA_SCHEMA" ]
"$OVPN" state doctor -j >/tmp/ovpn-doctor.out 2>/tmp/ovpn-doctor.err
if ! grep -Fq '"state": "EMPTY"' /tmp/ovpn-doctor.out; then
  echo 'doctor JSON output missing EMPTY state' >&2
  exit 1
fi
if [ -s /tmp/ovpn-doctor.err ]; then
  echo 'doctor emitted unexpected stderr output' >&2
  exit 1
fi

set +e
"$OVPN" does-not-exist >/tmp/ovpn-unknown.out 2>/tmp/ovpn-unknown.err
status=$?
set -e
if [ "$status" -ne 64 ]; then
  echo "unknown command returned $status, expected 64" >&2
  exit 1
fi

printf 'cli smoke passed\n'
