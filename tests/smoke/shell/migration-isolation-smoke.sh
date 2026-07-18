#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export OVPN_LIB_DIR="$LIB_DIR"
export OVPN_DATA_DIR="$TMP_DIR/current"
mkdir -p "$OVPN_DATA_DIR/config"
printf 'OVPN_CONFIG_VERSION=3\n' >"$OVPN_DATA_DIR/config/project.env"
printf '3\n' >"$OVPN_DATA_DIR/config/schema-version"

if grep -Eq 'ovpn_registry_(upgrade|write)_v1|1\|2' \
  "$LIB_DIR/config.sh" "$LIB_DIR/registry.sh" "$LIB_DIR/start.sh"; then
  echo 'current runtime contains a historical schema branch' >&2
  exit 1
fi
if grep -Fq 'ovpn upgrade' "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md" ||
  grep -Fq 'ovpn upgrade' "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md"; then
  echo 'data schema policy still assigns migration to upgrade' >&2
  exit 1
fi

trace="$TMP_DIR/help.trace"
BASH_XTRACEFD=3 bash -x "$OVPN" help 3>"$trace" >/dev/null 2>&1
if grep -Fq '/migrations/' "$trace" || grep -Fq '/migrate.sh' "$trace"; then
  echo 'normal CLI startup sourced migration code' >&2
  exit 1
fi

OVPN_MAINTENANCE=true "$OVPN" migrate plan --json >"$TMP_DIR/current.json"
grep -Fq '"source_schema":3' "$TMP_DIR/current.json"
grep -Fq '"chain":"none"' "$TMP_DIR/current.json"
OVPN_MAINTENANCE=true "$OVPN" migrate apply --yes
grep -Fqx '3' "$OVPN_DATA_DIR/config/schema-version"

set +e
"$OVPN" migrate plan >"$TMP_DIR/live.out" 2>"$TMP_DIR/live.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'only through the openvpn-maintenance service' "$TMP_DIR/live.err"

for fixture in conflict newer; do
  export OVPN_DATA_DIR="$TMP_DIR/$fixture"
  mkdir -p "$OVPN_DATA_DIR/config"
  if [ "$fixture" = conflict ]; then
    printf 'OVPN_CONFIG_VERSION=2\n' >"$OVPN_DATA_DIR/config/project.env"
    printf '3\n' >"$OVPN_DATA_DIR/config/schema-version"
    expected='schema status is CONFLICT'
  else
    printf 'OVPN_CONFIG_VERSION=4\n' >"$OVPN_DATA_DIR/config/project.env"
    printf '4\n' >"$OVPN_DATA_DIR/config/schema-version"
    expected='schema status is NEWER'
  fi
  for command in plan apply; do
    set +e
    if [ "$command" = plan ]; then
      OVPN_MAINTENANCE=true "$OVPN" migrate plan \
        >"$TMP_DIR/$fixture-$command.out" 2>"$TMP_DIR/$fixture-$command.err"
    else
      OVPN_MAINTENANCE=true "$OVPN" migrate apply --yes \
        >"$TMP_DIR/$fixture-$command.out" 2>"$TMP_DIR/$fixture-$command.err"
    fi
    status=$?
    set -e
    [ "$status" -eq 78 ]
    if [ "$command" = plan ]; then
      grep -Fq "$expected" "$TMP_DIR/$fixture-$command.out"
    else
      grep -Fq 'cannot migrate while schema status is' \
        "$TMP_DIR/$fixture-$command.err"
    fi
  done
done

printf 'migration isolation smoke passed\n'
