#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"

write_schema() {
  local data_dir="$1"
  local project_version="$2"
  local file_version="$3"

  mkdir -p "$data_dir/config"
  printf 'OVPN_CONFIG_VERSION=%s\n' "$project_version" >"$data_dir/config/project.env"
  printf '%s\n' "$file_version" >"$data_dir/config/schema-version"
}

old="$TMP_DIR/old"
write_schema "$old" 1 1
for args in \
  'start' \
  'config show' \
  'client list' \
  'network plan' \
  'repair plan' \
  'state show' \
  'render server' \
  'runtime status' \
  'upgrade --check'
do
  read -ra command_args <<<"$args"
  set +e
  OVPN_DATA_DIR="$old" "$OVPN" "${command_args[@]}" >"$TMP_DIR/old.out" 2>"$TMP_DIR/old.err"
  status=$?
  set -e
  [ "$status" -eq 78 ]
  grep -Fq 'data schema migration required' "$TMP_DIR/old.err"
done

OVPN_DATA_DIR="$old" "$OVPN" help >/dev/null
OVPN_DATA_DIR="$old" OVPN_BUILD_INFO="$TMP_DIR/missing-build-info" "$OVPN" --version >/dev/null
set +e
OVPN_DATA_DIR="$old" "$OVPN" migrate plan >"$TMP_DIR/migrate.out" 2>"$TMP_DIR/migrate.err"
status=$?
set -e
[ "$status" -eq 64 ]
grep -Fq "unknown command 'migrate'" "$TMP_DIR/migrate.err"

current_incomplete="$TMP_DIR/current-incomplete"
mkdir -p "$current_incomplete/config"
printf 'OVPN_CONFIG_VERSION=2\n' >"$current_incomplete/config/project.env"
set +e
OVPN_DATA_DIR="$current_incomplete" "$OVPN" config show >"$TMP_DIR/incomplete.out" 2>"$TMP_DIR/incomplete.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'metadata is incomplete' "$TMP_DIR/incomplete.err"

conflict="$TMP_DIR/conflict"
write_schema "$conflict" 1 2
set +e
OVPN_DATA_DIR="$conflict" "$OVPN" state show >"$TMP_DIR/conflict.out" 2>"$TMP_DIR/conflict.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'metadata conflicts' "$TMP_DIR/conflict.err"

newer="$TMP_DIR/newer"
write_schema "$newer" 3 3
set +e
OVPN_DATA_DIR="$newer" "$OVPN" state show >"$TMP_DIR/newer.out" 2>"$TMP_DIR/newer.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'newer than this image supports' "$TMP_DIR/newer.err"

printf 'schema gate smoke passed\n'
