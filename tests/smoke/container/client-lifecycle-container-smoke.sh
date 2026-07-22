#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_LIFECYCLE_IMAGE:-szcq/openvpn-server:lifecycle-smoke}"
REQUIRED="${OVPN_LIFECYCLE_REQUIRED:-0}"
SKIP_BUILD="${OVPN_LIFECYCLE_SKIP_BUILD:-0}"
WORK_DIR=''

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'client lifecycle smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'client lifecycle smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
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

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-lifecycle.XXXXXX")"
mkdir -m 0750 "$WORK_DIR/data" "$WORK_DIR/config"
cat >"$WORK_DIR/config/config.yaml" <<'YAML'
version: 1
server:
  endpoint: vpn.example.test
  transport:
    protocol: udp
    family: auto
    port: 1194
  clientToClient: true
ipv4:
  network: 10.70.0.0/24
  dynamicPoolSize: 64
  nat:
    enabled: false
    interface: auto
  redirectGateway: false
  dns: []
  routes: []
logging:
  maxBytes: 10485760
  backups: 5
YAML

run_ovpn() {
  docker run --rm \
    -v "$WORK_DIR/data:/etc/openvpn" \
    -v "$WORK_DIR/config:/etc/ovpn-conf" \
    --entrypoint ovpn \
    "$IMAGE" "$@"
}

run_ovpn server init >"$WORK_DIR/init.out"
run_ovpn client create alpha --ipv4 auto --output /etc/openvpn/alpha-created.ovpn --json >"$WORK_DIR/create.json"
client_id="$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$WORK_DIR/create.json")"
[[ "$client_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]
grep -Fq '"version":1' "$WORK_DIR/create.json"
grep -Fq '"profile_redistribution_required":true' "$WORK_DIR/create.json"
grep -Fq '"profile_output":{"destination":"/etc/openvpn/alpha-created.ovpn","written":true}' "$WORK_DIR/create.json"
test -s "$WORK_DIR/data/alpha-created.ovpn"
prefix="${client_id:0:8}"

run_ovpn client export --id "$prefix" --output - >"$WORK_DIR/alpha.ovpn"
grep -Fq "# ovpn-client-id: $client_id" "$WORK_DIR/alpha.ovpn"
run_ovpn client rename --id "$prefix" beta --json >"$WORK_DIR/rename.json"
grep -Fq '"profile_redistribution_required":true' "$WORK_DIR/rename.json"
run_ovpn client address set --name beta --ipv4 10.70.0.10 --json >"$WORK_DIR/address-set.json"
grep -Fq "\"kick_required\":[\"$client_id\"]" "$WORK_DIR/address-set.json"
grep -Fq '"status":"pending"' "$WORK_DIR/address-set.json"
run_ovpn client address edit beta -e /bin/true --yes --json >"$WORK_DIR/address-edit.json"
grep -Fq "\"id\":\"$client_id\"" "$WORK_DIR/address-edit.json"
run_ovpn client revoke --name beta --release-ipv4 --json >"$WORK_DIR/revoke.json"
grep -Fq '"kick_required":true' "$WORK_DIR/revoke.json"
grep -Fq '"runtime":{"client_id"' "$WORK_DIR/revoke.json"
grep -Fq '"status":"pending"' "$WORK_DIR/revoke.json"
run_ovpn client reissue --id "$prefix" --ipv4 dynamic --output /etc/openvpn/beta-reissued.ovpn --json >"$WORK_DIR/reissue.json"
grep -Fq '"profile_redistribution_required":true' "$WORK_DIR/reissue.json"
grep -Fq '"profile_output":{"destination":"/etc/openvpn/beta-reissued.ovpn","written":true}' "$WORK_DIR/reissue.json"
grep -Fq '"status":"pending"' "$WORK_DIR/reissue.json"
test -s "$WORK_DIR/data/beta-reissued.ovpn"
run_ovpn client delete --id "$prefix" --yes --json >"$WORK_DIR/delete.json"
grep -Fq '"status":"deleted"' "$WORK_DIR/delete.json"
grep -Fq '"runtime":{"client_id"' "$WORK_DIR/delete.json"
run_ovpn client create beta --ipv4 auto --json >"$WORK_DIR/reuse.json"
replacement_id="$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$WORK_DIR/reuse.json")"
test -n "$replacement_id"
test "$replacement_id" != "$client_id"
run_ovpn client list --detail --json >"$WORK_DIR/list.json"
grep -Fq "\"id\":\"$replacement_id\"" "$WORK_DIR/list.json"
grep -Fq '"ipv4":{"mode":"static","address":"10.70.0.2","state":"configured"}' "$WORK_DIR/list.json"
run_ovpn client list --detail >"$WORK_DIR/list-detail.txt"
test "$(head -n 1 "$WORK_DIR/list-detail.txt" | tr -s ' ')" = 'CLIENT ID NAME STATUS CONNECTION IPV4 MODE IPV4 ADDRESS IPV4 STATE'
grep -Fq 'configured' "$WORK_DIR/list-detail.txt"
run_ovpn state doctor --json >"$WORK_DIR/doctor.json"
grep -Fq '"state":"HEALTHY"' "$WORK_DIR/doctor.json"

printf 'Go client lifecycle smoke passed (tombstone=%s replacement=%s)\n' "$client_id" "$replacement_id"
