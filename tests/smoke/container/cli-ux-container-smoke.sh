#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_CLI_UX_IMAGE:-szcq/openvpn-server:cli-ux-smoke}"
REQUIRED="${OVPN_CLI_UX_REQUIRED:-0}"
SKIP_BUILD="${OVPN_CLI_UX_SKIP_BUILD:-0}"
WORK_DIR=''

set -a
# shellcheck source=../../../versions.env
. "$ROOT_DIR/versions.env"
set +a

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'CLI UX smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'CLI UX smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint sh "$IMAGE" \
      -ec 'find /work -mindepth 1 -delete' >/dev/null 2>&1 || true
    rmdir "$WORK_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'

if [ "$SKIP_BUILD" != 1 ]; then
  OVPN_BUILD_NETWORK=host "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-cli-ux.XXXXXX")"
mkdir -m 0750 "$WORK_DIR/data" "$WORK_DIR/config"
cp "$ROOT_DIR/config.example.yaml" "$WORK_DIR/config/config.yaml"

run_image() {
  docker run --rm --entrypoint ovpn "$IMAGE" "$@"
}

run_ovpn() {
  docker run --rm \
    -v "$WORK_DIR/data:/etc/openvpn" \
    -v "$WORK_DIR/config:/etc/openvpn-config" \
    --entrypoint ovpn \
    "$IMAGE" "$@"
}

test "$(run_image -v)" = "$GO_RUNTIME_VERSION"
run_image -V | grep -Fq "ovpn $GO_RUNTIME_VERSION"
run_image --version | grep -Fq "data schema: $DATA_SCHEMA"

root_help="$(run_image --help)"
grep -Fq 'completion' <<<"$root_help"
test "$root_help" = "$(run_image help)"
run_image completion bash | grep -Fq "help --version"

while IFS= read -r path; do
  read -r -a args <<<"$path"
  by_topic="$(run_image help "${args[@]}")"
  by_flag="$(run_image "${args[@]}" -h)"
  if [ "$by_topic" != "$by_flag" ]; then
    printf 'help forms differ for: %s\n' "$path" >&2
    exit 1
  fi
done <<'PATHS'
server
server init
server run
server render
config
config validate
config show
config export
config plan
config apply
client
client create
client list
client export
client rename
client revoke
client reissue
client delete
client address
client address set
client address edit
client address release
state
state show
state doctor
repair
repair plan
repair apply
migrate
migrate plan
migrate apply
runtime
runtime status
runtime disconnect
runtime health
runtime capabilities
runtime logs
runtime events
completion
version
PATHS

run_image completion bash >"$WORK_DIR/ovpn.bash"
bash -n "$WORK_DIR/ovpn.bash"
grep -Fq -- '--release-ipv4' "$WORK_DIR/ovpn.bash"
grep -Fq -- '--full-id' "$WORK_DIR/ovpn.bash"
bash -ec 'source "$1"; COMP_WORDS=(ovpn help client ""); COMP_CWORD=3; _ovpn_completion; printf "%s\n" "${COMPREPLY[@]}"' \
  _ "$WORK_DIR/ovpn.bash" >"$WORK_DIR/help-client-completion.out"
grep -Fxq create "$WORK_DIR/help-client-completion.out"
grep -Fxq address "$WORK_DIR/help-client-completion.out"

run_image completion zsh >"$WORK_DIR/_ovpn"
if command -v zsh >/dev/null 2>&1; then
  zsh -n "$WORK_DIR/_ovpn"
fi
run_image completion fish >"$WORK_DIR/ovpn.fish"
if command -v fish >/dev/null 2>&1; then
  fish -n "$WORK_DIR/ovpn.fish"
fi

run_ovpn config validate --json >"$WORK_DIR/validate.json"
grep -Fq '"valid":true' "$WORK_DIR/validate.json"
grep -Fq '"network":"10.42.0.0/24"' "$WORK_DIR/validate.json"

run_ovpn server init >"$WORK_DIR/init.out"
test "$(run_ovpn client)" = "$(run_ovpn client list)"
test "$(run_ovpn client)" = 'No clients.'
test "$(run_ovpn state)" = "$(run_ovpn state doctor)"

set +e
run_ovpn runtime >"$WORK_DIR/runtime-default.out" 2>"$WORK_DIR/runtime-default.err"
runtime_default_code=$?
run_ovpn runtime status >"$WORK_DIR/runtime-status.out" 2>"$WORK_DIR/runtime-status.err"
runtime_status_code=$?
set -e
test "$runtime_default_code" -eq "$runtime_status_code"
diff -u "$WORK_DIR/runtime-default.out" "$WORK_DIR/runtime-status.out"
diff -u "$WORK_DIR/runtime-default.err" "$WORK_DIR/runtime-status.err"

run_ovpn config apply --json >"$WORK_DIR/noop-apply.json"
grep -Fq '"applied":false' "$WORK_DIR/noop-apply.json"

set +e
run_image completion powershell >"$WORK_DIR/invalid.out" 2>"$WORK_DIR/invalid.err"
invalid_code=$?
set -e
test "$invalid_code" -eq 64
grep -Fq 'expected bash, zsh, or fish' "$WORK_DIR/invalid.err"
grep -Fq 'run the command with -h' "$WORK_DIR/invalid.err"

printf 'CLI UX container smoke passed\n'
