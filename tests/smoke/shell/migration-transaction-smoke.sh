#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
TMP_DIR="$(mktemp -d)"
holder_pid=''

cleanup() {
  if [ -n "$holder_pid" ]; then
    kill "$holder_pid" >/dev/null 2>&1 || true
    wait "$holder_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

export OVPN_LIB_DIR="$LIB_DIR"
export OVPN_DATA_DIR="$TMP_DIR/data"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_MAINTENANCE=true
export OVPN_SERVER_NAME=openvpn-server
mkdir -p "$OVPN_DATA_DIR/config" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/repair/.scripts"
printf 'OVPN_CONFIG_VERSION=2\n' >"$OVPN_DATA_DIR/config/project.env"
printf '2\n' >"$OVPN_DATA_DIR/config/schema-version"
printf 'preserve\n' >"$OVPN_DATA_DIR/meta/value"
printf 'trusted-bundle\n' >"$OVPN_DATA_DIR/repair/.scripts/sentinel"

# shellcheck source=../../../rootfs/usr/local/lib/openvpn-container/common.sh
. "$LIB_DIR/common.sh"
. "$LIB_DIR/lock.sh"
. "$LIB_DIR/schema.sh"
. "$LIB_DIR/metadata.sh"
. "$LIB_DIR/migrate.sh"

migrate_to_3() {
  local stage="$1"

  printf 'OVPN_CONFIG_VERSION=3\n' >"$stage/config/project.env"
  printf '3\n' >"$stage/config/schema-version"
  printf 'migrated\n' >"$stage/meta/value"
}

validate_3() {
  local data="$1"

  grep -Fqx 'OVPN_CONFIG_VERSION=3' "$data/config/project.env"
  grep -Fqx '3' "$data/config/schema-version"
  grep -Fqx 'migrated' "$data/meta/value"
}

expect_source_2_then_migrate() {
  local stage="$1"

  grep -Fqx 'OVPN_CONFIG_VERSION=2' "$stage/config/project.env"
  migrate_to_3 "$stage"
}

set +e
OVPN_MAINTENANCE=false ovpn_migration_transaction_run 2 3 migrate_to_3 validate_3 \
  >"$TMP_DIR/not-maintenance.out" 2>"$TMP_DIR/not-maintenance.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'only through the openvpn-maintenance service' "$TMP_DIR/not-maintenance.err"

ovpn_with_runtime_shared_lock sleep 2 &
holder_pid=$!
for _ in $(seq 1 50); do
  if ! flock -x -n "$(ovpn_runtime_lock_file)" true; then
    break
  fi
  sleep 0.1
done
if flock -x -n "$(ovpn_runtime_lock_file)" true; then
  echo 'shared runtime lock holder did not start' >&2
  exit 1
fi
set +e
ovpn_migration_transaction_run 2 3 migrate_to_3 validate_3 \
  >"$TMP_DIR/running.out" 2>"$TMP_DIR/running.err"
status=$?
set -e
[ "$status" -eq 78 ]
grep -Fq 'stop the openvpn service before migration' "$TMP_DIR/running.err"
wait "$holder_pid"
holder_pid=''

ovpn_migration_transaction_run 2 3 migrate_to_3 validate_3
validate_3 "$OVPN_DATA_DIR"
grep -Fqx 'trusted-bundle' "$OVPN_DATA_DIR/repair/.scripts/sentinel"
[ ! -e "$OVPN_DATA_DIR/repair/migrations/transaction.env" ]
grep -Fq '"result":"success"' "$OVPN_DATA_DIR"/repair/migrations/reports/*.json
test -s "$OVPN_DATA_DIR"/repair/migrations/snapshots/*.tar.gz

printf 'OVPN_CONFIG_VERSION=2\n' >"$OVPN_DATA_DIR/config/project.env"
printf '2\n' >"$OVPN_DATA_DIR/config/schema-version"
printf 'preserve\n' >"$OVPN_DATA_DIR/meta/value"
set +e
OVPN_MIGRATION_FAIL_AFTER_COMMIT=config \
  ovpn_migration_transaction_run 2 3 migrate_to_3 validate_3 \
  >"$TMP_DIR/failure.out" 2>"$TMP_DIR/failure.err"
status=$?
set -e
[ "$status" -eq 1 ]
grep -Fqx 'OVPN_CONFIG_VERSION=2' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx '2' "$OVPN_DATA_DIR/config/schema-version"
grep -Fqx 'preserve' "$OVPN_DATA_DIR/meta/value"
grep -Fqx 'trusted-bundle' "$OVPN_DATA_DIR/repair/.scripts/sentinel"
[ ! -e "$OVPN_DATA_DIR/repair/migrations/transaction.env" ]
grep -Fq '"result":"failed"' "$OVPN_DATA_DIR"/repair/migrations/reports/*.json

ovpn_migration_transaction_start 2 3
printf 'tampered\n' >>"$OVPN_MIGRATION_TRANSACTION_SNAPSHOT"
printf 'OVPN_CONFIG_VERSION=999\n' >"$OVPN_DATA_DIR/config/project.env"
set +e
(ovpn_migration_transaction_recover_interrupted) \
  >"$TMP_DIR/tampered.out" 2>"$TMP_DIR/tampered.err"
status=$?
set -e
[ "$status" -eq 1 ]
grep -Fq 'snapshot checksum mismatch' "$TMP_DIR/tampered.err"
grep -Fqx 'OVPN_CONFIG_VERSION=999' "$OVPN_DATA_DIR/config/project.env"
[ -e "$OVPN_DATA_DIR/repair/migrations/transaction.env" ]
rm -f "$OVPN_DATA_DIR/repair/migrations/transaction.env"
rm -rf "$(dirname "$OVPN_MIGRATION_TRANSACTION_STAGE")"

printf 'OVPN_CONFIG_VERSION=2\n' >"$OVPN_DATA_DIR/config/project.env"
printf '2\n' >"$OVPN_DATA_DIR/config/schema-version"
printf 'preserve\n' >"$OVPN_DATA_DIR/meta/value"
ovpn_migration_transaction_start 2 3
printf 'OVPN_CONFIG_VERSION=999\n' >"$OVPN_DATA_DIR/config/project.env"
printf '999\n' >"$OVPN_DATA_DIR/config/schema-version"
printf 'interrupted\n' >"$OVPN_DATA_DIR/meta/value"
ovpn_migration_transaction_run 2 3 expect_source_2_then_migrate validate_3
validate_3 "$OVPN_DATA_DIR"
[ ! -e "$OVPN_DATA_DIR/repair/migrations/transaction.env" ]
grep -Fq '"result":"recovered"' "$OVPN_DATA_DIR"/repair/migrations/reports/*.json

printf 'migration transaction smoke passed\n'
