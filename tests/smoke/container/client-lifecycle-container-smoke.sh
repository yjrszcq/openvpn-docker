#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_LIFECYCLE_IMAGE:-szcq/openvpn-server:lifecycle-smoke}"
REQUIRED="${OVPN_LIFECYCLE_REQUIRED:-0}"
SKIP_BUILD="${OVPN_LIFECYCLE_SKIP_BUILD:-0}"
NETWORK="10.88.0.0/24"
WORK_DIR=""

skip_or_fail() {
  local reason="$1"

  if [ "$REQUIRED" = 1 ]; then
    printf 'client lifecycle container smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'client lifecycle container smoke skipped: %s\n' "$reason"
  exit 0
}

cleanup() {
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
  skip_or_fail 'missing command: docker'
fi
if ! docker info >/dev/null 2>&1; then
  skip_or_fail 'Docker daemon is not accessible'
fi

if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-lifecycle.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"

run_control() {
  docker run --rm \
    -e OVPN_ENDPOINT=lifecycle.example.test \
    -e OVPN_NETWORK="$NETWORK" \
    -e OVPN_PROTO=udp \
    -v "$data_dir:/etc/openvpn" \
    "$IMAGE" \
    "$@"
}

client='lifecycle-client'
run_control init >/tmp/ovpn-lifecycle-init.out 2>/tmp/ovpn-lifecycle-init.err
run_control client create "$client" --dynamic >/tmp/ovpn-lifecycle-create.out 2>/tmp/ovpn-lifecycle-create.err
client_id="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/awk "$IMAGE" -F, -v client="$client" '$2 == client { print $1 }' /etc/openvpn/meta/client-state.csv)"
[[ "$client_id" =~ ^[0-9a-f-]{36}$ ]]
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec '
  openssl x509 -in "/etc/openvpn/pki/issued/$1.crt" -noout -subject -nameopt RFC2253 | grep -Fq "CN=$1"
  grep -Fqx "# ovpn-client-id: $1" "/etc/openvpn/clients/active/$2.ovpn"
  grep -Fqx "# ovpn-client-name: $2" "/etc/openvpn/clients/active/$2.ovpn"
' sh "$client_id" "$client"

assignment_before="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/awk "$IMAGE" -F, -v client="$client" '$2 == client { print; exit }' /etc/openvpn/data/client-ip.csv)"
[ "$assignment_before" = "$client_id,$client," ] || {
  printf 'unexpected dynamic assignment before reissue: %s\n' "$assignment_before" >&2
  exit 1
}
key_before="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$IMAGE" "/etc/openvpn/pki/private/$client_id.key")"
index_before="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$IMAGE" /etc/openvpn/pki/index.txt)"

if ! run_control client reissue "$client_id" --dynamic >/tmp/ovpn-lifecycle-reissue.out 2>/tmp/ovpn-lifecycle-reissue.err; then
  echo 'same-CN reissue unexpectedly failed in the shipped Easy-RSA runtime' >&2
  sed 's/^/  | /' /tmp/ovpn-lifecycle-reissue.err >&2
  exit 1
fi
grep -E "^${client}[[:space:]]+${client_id}[[:space:]]+active$" <(run_control client list)
assignment_after="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/awk "$IMAGE" -F, -v client="$client" '$2 == client { print; exit }' /etc/openvpn/data/client-ip.csv)"
[ "$assignment_after" = "$assignment_before" ] || {
  printf 'reissue changed the IP assignment: %s\n' "$assignment_after" >&2
  exit 1
}
key_after="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$IMAGE" "/etc/openvpn/pki/private/$client_id.key")"
index_after="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/sha256sum "$IMAGE" /etc/openvpn/pki/index.txt)"
[ "$key_after" != "$key_before" ] || {
  echo 'same-CN reissue did not generate a new key' >&2
  exit 1
}
[ "$index_after" != "$index_before" ] || {
  echo 'same-CN reissue did not update the PKI index' >&2
  exit 1
}

run_control client ip set "$client_id" --ip 10.88.0.2 >/tmp/ovpn-lifecycle-static.out 2>/tmp/ovpn-lifecycle-static.err
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/grep "$IMAGE" -Fqx "$client_id,$client,10.88.0.2" /etc/openvpn/data/client-ip.csv
run_control client revoke "$client_id" >/tmp/ovpn-lifecycle-revoke.out 2>/tmp/ovpn-lifecycle-revoke.err
grep -E "^${client}[[:space:]]+${client_id}[[:space:]]+revoked$" <(run_control client list)
run_control client ip release "$client_id" >/tmp/ovpn-lifecycle-release-ip.out 2>/tmp/ovpn-lifecycle-release-ip.err
grep -E "^${client}[[:space:]]+${client_id}[[:space:]]+revoked$" <(run_control client list)
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/grep "$IMAGE" -Fqx "$client_id,$client," /etc/openvpn/data/client-ip.csv
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/test "$IMAGE" -f "/etc/openvpn/clients/revoked/$client.ovpn"
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/test "$IMAGE" -f "/etc/openvpn/pki/private/$client_id.key"
run_control client delete "$client_id" >/tmp/ovpn-lifecycle-delete.out 2>/tmp/ovpn-lifecycle-delete.err
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec '
  ! grep -Fq "$1,$2," /etc/openvpn/data/client-ip.csv
  grep -Fqx "$1,$2,deleted" /etc/openvpn/meta/client-state.csv
  test ! -e "/etc/openvpn/pki/private/$1.key"
  test ! -e "/etc/openvpn/clients/active/$2.ovpn"
  test ! -e "/etc/openvpn/clients/revoked/$2.ovpn"
' sh "$client_id" "$client"
docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/sh "$IMAGE" -ec '
  test -f /etc/openvpn/meta/audit.jsonl
  ! grep -E -- "-----BEGIN [A-Z ]*PRIVATE KEY-----" /etc/openvpn/meta/audit.jsonl
'

# reissue no-IP client without params: should auto-allocate a static IP
run_control client create keep-dynamic --dynamic >/tmp/ovpn-lifecycle-create-dynamic.out 2>/tmp/ovpn-lifecycle-create-dynamic.err
keep_dynamic_id="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/awk "$IMAGE" -F, '$2 == "keep-dynamic" { print $1 }' /etc/openvpn/meta/client-state.csv)"
run_control client revoke keep-dynamic --release-ip >/tmp/ovpn-lifecycle-revoke-dynamic.out 2>/tmp/ovpn-lifecycle-revoke-dynamic.err
run_control client reissue keep-dynamic >/tmp/ovpn-lifecycle-reissue-dynamic.out 2>/tmp/ovpn-lifecycle-reissue-dynamic.err
assignment_static="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/awk "$IMAGE" -F, -v client=keep-dynamic '$2 == client { print; exit }' /etc/openvpn/data/client-ip.csv)"
grep -q "^$keep_dynamic_id,keep-dynamic,10\\.88\\.0\\." <<<"$assignment_static" || {
  printf 'reissue without params did not auto-allocate a static IP for a no-IP client: %s\n' "$assignment_static" >&2
  exit 1
}

printf 'client lifecycle container smoke passed (network=%s)\n' "$NETWORK"
