#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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
assert_help "Usage: ovpn client create <name> [--dynamic|--ip <IPv4>]" client create --help
assert_help "Usage: ovpn client ip <command> [args]" client ip -h
assert_help "Usage: ovpn client ip set-static <client...|--all> [--ip <IPv4>]" client ip set-static --help
assert_help "Usage: ovpn network <command> [options]" network --help
assert_help "Usage: ovpn network apply [--network <CIDR>] [--dynamic-pool-size <N>] [--yes]" network apply -h
assert_help "Usage: ovpn repair <command>" repair --help
assert_help "Usage: ovpn repair plan [--json]" repair plan -h
assert_help "Usage: ovpn state <command>" state --help
assert_help "Usage: ovpn state doctor [--json]" state doctor -h
assert_help "Usage: ovpn render <target> [options]" render --help
assert_help "Usage: ovpn render client <name> [--stdout|--output <path>]" render client -h
assert_help "Usage: ovpn runtime <command>" runtime --help
assert_help "Usage: ovpn runtime version" runtime version -h

assert_help "Usage: ovpn config show" config show --help
assert_help "Usage: ovpn client export <name>" client export -h
assert_help "Usage: ovpn client list [--detail]" client list --help
assert_help "Usage: ovpn client revoke <name> [--release-ip]" client revoke -h
assert_help "Usage: ovpn client ip release <name>" client ip release --help
assert_help "Usage: ovpn client reissue <name>" client reissue -h
assert_help "Usage: ovpn client delete <name>" client delete --help
assert_help "Usage: ovpn client ip list" client ip list -h
assert_help "Usage: ovpn client ip validate" client ip validate --help
assert_help "Usage: ovpn client ip apply" client ip apply -h
assert_help "Usage: ovpn client ip edit" client ip edit --help
assert_help "Usage: ovpn client ip set-dynamic <client...|--all>" client ip set-dynamic -h
assert_help "Usage: ovpn network plan [--network <CIDR>] [--dynamic-pool-size <N>]" network plan --help
assert_help "Usage: ovpn repair apply" repair apply -h
assert_help "Usage: ovpn state show" state show --help
assert_help "Usage: ovpn render server [--stdout|--output <path>]" render server -h
assert_help "Usage: ovpn runtime status" runtime status --help
assert_help "Usage: ovpn runtime health" runtime health -h
assert_help "Usage: ovpn runtime capabilities" runtime capabilities --help

"$OVPN" runtime version >/tmp/ovpn-version.out
if ! grep -Fq "\"image_version\": \"$IMAGE_VERSION\"" /tmp/ovpn-version.out; then
  echo 'version output missing image_version' >&2
  exit 1
fi
if ! grep -Fq "\"openvpn_source_version\": \"$OPENVPN_VERSION\"" /tmp/ovpn-version.out; then
  echo 'version output missing openvpn_source_version' >&2
  exit 1
fi
export OVPN_DATA_DIR="$data_dir"
"$OVPN" state doctor --json >/tmp/ovpn-doctor.out 2>/tmp/ovpn-doctor.err
if ! grep -Fq '"state": "EMPTY"' /tmp/ovpn-doctor.out; then
  echo 'doctor JSON output missing EMPTY state' >&2
  exit 1
fi
if [ -s /tmp/ovpn-doctor.err ]; then
  echo 'doctor emitted unexpected stderr output' >&2
  exit 1
fi

assert_retired_command() {
  if "$OVPN" "$@" >/tmp/ovpn-retired.out 2>/tmp/ovpn-retired.err; then
    echo "retired command unexpectedly succeeded: ovpn $*" >&2
    exit 1
  fi
}

assert_retired_command add-client
assert_retired_command client-ip
assert_retired_command client set-static
assert_retired_command client ip sync
assert_retired_command config print
assert_retired_command network reconfigure
assert_retired_command repair --plan
assert_retired_command doctor
assert_retired_command status
assert_retired_command healthcheck
assert_retired_command capabilities
assert_retired_command version
assert_retired_command recover

set +e
"$OVPN" does-not-exist >/tmp/ovpn-unknown.out 2>/tmp/ovpn-unknown.err
status=$?
set -e
if [ "$status" -ne 64 ]; then
  echo "unknown command returned $status, expected 64" >&2
  exit 1
fi

printf 'cli smoke passed\n'
