#!/usr/bin/env bash
set -euo pipefail

IMAGE="${OVPN_SCHEMA_MIGRATION_IMAGE:-szcq/openvpn-server:schema-migration-smoke}"
REQUIRED="${OVPN_SCHEMA_MIGRATION_REQUIRED:-0}"
SKIP_BUILD="${OVPN_SCHEMA_MIGRATION_SKIP_BUILD:-0}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK_DIR=''
CONTAINER="ovpn-schema-migration-$$"

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'schema migration container smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'schema migration container smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'
[ -c /dev/net/tun ] || skip_or_fail 'host /dev/net/tun is not available'
if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-schema-migration.XXXXXX")"
data_dir="$WORK_DIR/data"
mkdir -p "$data_dir"
docker run --rm \
  -v "$data_dir:/etc/openvpn" \
  --entrypoint /bin/bash \
  "$IMAGE" -ec '
    export EASYRSA_BATCH=1 EASYRSA_PKI=/etc/openvpn/pki
    easyrsa=/usr/share/easy-rsa/easyrsa
    "$easyrsa" init-pki
    EASYRSA_REQ_CN="Migration Test CA" "$easyrsa" build-ca nopass
    EASYRSA_REQ_CN=openvpn-server "$easyrsa" build-server-full openvpn-server nopass
    EASYRSA_REQ_CN=alpha "$easyrsa" build-client-full alpha nopass
    EASYRSA_REQ_CN=beta "$easyrsa" build-client-full beta nopass
    "$easyrsa" revoke beta
    "$easyrsa" gen-crl
    mkdir -p /etc/openvpn/config /etc/openvpn/data/leases /etc/openvpn/meta \
      /etc/openvpn/clients/active /etc/openvpn/clients/revoked /etc/openvpn/ccd \
      /etc/openvpn/server /etc/openvpn/secrets /etc/openvpn/repair/.scripts
    cp /etc/openvpn/pki/issued/alpha.crt /etc/openvpn/repair/old-alpha.crt
    {
      printf "OVPN_CONFIG_VERSION=2\n"
      printf "OVPN_ENDPOINT=migration.example.test\n"
      printf "OVPN_PROTO=udp\n"
      printf "OVPN_TRANSPORT_FAMILY=auto\n"
      printf "OVPN_PORT=1194\n"
      printf "OVPN_NETWORK=10.91.0.0/24\n"
      printf "OVPN_TOPOLOGY=subnet\n"
      printf "OVPN_DYNAMIC_POOL_SIZE=126\n"
      printf "OVPN_NAT=false\n"
      printf "OVPN_NAT_INTERFACE=auto\n"
      printf "OVPN_REDIRECT_GATEWAY=false\n"
      printf "OVPN_CLIENT_TO_CLIENT=true\n"
      printf "OVPN_DNS=\n"
      printf "OVPN_ROUTES=\n"
    } >/etc/openvpn/config/project.env
    printf "2\n" >/etc/openvpn/config/schema-version
    printf "# client,state\nalpha,active\nbeta,revoked\n" >/etc/openvpn/meta/client-state.csv
    printf "# client,ip\nalpha,10.91.0.2\nbeta,\n" >/etc/openvpn/data/client-ip.csv
    cp /etc/openvpn/data/client-ip.csv /etc/openvpn/meta/client-ip.applied.csv
    printf '"'"'{"timestamp":"2026-01-01T00:00:00Z","operation":"revoke","result":"applied"}\n'"'"' \
      >/etc/openvpn/meta/audit.jsonl
    openvpn --genkey secret /etc/openvpn/secrets/tls-crypt.key
    printf "old alpha profile\n" >/etc/openvpn/clients/active/alpha.ovpn
    printf "old beta profile\n" >/etc/openvpn/clients/revoked/beta.ovpn
    printf "trusted bundle\n" >/etc/openvpn/repair/.scripts/sentinel
    chmod 600 /etc/openvpn/config/project.env /etc/openvpn/config/schema-version \
      /etc/openvpn/data/client-ip.csv /etc/openvpn/meta/client-ip.applied.csv \
      /etc/openvpn/meta/client-state.csv /etc/openvpn/meta/audit.jsonl \
      /etc/openvpn/secrets/tls-crypt.key
  '

docker run --rm \
  -e OVPN_MAINTENANCE=true \
  -v "$data_dir:/etc/openvpn" \
  "$IMAGE" migrate plan --json >"$WORK_DIR/plan.json"
grep -Fq '"source_schema":2' "$WORK_DIR/plan.json"
grep -Fq '"chain":"2-to-3"' "$WORK_DIR/plan.json"
docker run --rm \
  -e OVPN_MAINTENANCE=true \
  -v "$data_dir:/etc/openvpn" \
  "$IMAGE" migrate apply --yes >"$WORK_DIR/apply.out"

alpha_id="$(
  docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/awk "$IMAGE" \
    -F, '$2 == "alpha" && $3 == "active" { print $1 }' /etc/openvpn/meta/client-state.csv
)"
beta_id="$(
  docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/awk "$IMAGE" \
    -F, '$2 == "beta" && $3 == "revoked" { print $1 }' /etc/openvpn/meta/client-state.csv
)"
[[ "$alpha_id" =~ ^[0-9a-f-]{36}$ ]]
[[ "$beta_id" =~ ^[0-9a-f-]{36}$ ]]
grep -Fq 'redistribute profile:' "$WORK_DIR/apply.out"

docker run --rm \
  -e ALPHA_ID="$alpha_id" \
  -e BETA_ID="$beta_id" \
  -v "$data_dir:/etc/openvpn:ro" \
  --entrypoint /bin/bash \
  "$IMAGE" -ec '
    grep -Fqx "$ALPHA_ID,alpha,10.91.0.2" /etc/openvpn/data/client-ip.csv
    grep -Fqx "$BETA_ID,beta," /etc/openvpn/data/client-ip.csv
    grep -Fqx '"'"'{"timestamp":"2026-01-01T00:00:00Z","event":"client_lifecycle","operation":"revoke","outcome":"applied","client_id":null,"client_name":null,"legacy":true,"source_schema":2}'"'"' \
      /etc/openvpn/meta/audit.jsonl
    grep -Fqx "# ovpn-client-id: $ALPHA_ID" /etc/openvpn/clients/active/alpha.ovpn
    grep -Fqx "# ovpn-client-id: $BETA_ID" /etc/openvpn/clients/revoked/beta.ovpn
    awk "/<cert>/{capture=1;next} /<\\/cert>/{capture=0} capture" \
      /etc/openvpn/clients/revoked/beta.ovpn >/tmp/beta.crt
    openssl x509 -in "/etc/openvpn/pki/issued/$ALPHA_ID.crt" -noout -subject |
      grep -Eq "CN ?= ?$ALPHA_ID"
    openssl x509 -in /tmp/beta.crt -noout -subject |
      grep -Eq "CN ?= ?$BETA_ID"
    openssl crl -in /etc/openvpn/pki/crl.pem -noout
    openssl verify -CAfile /etc/openvpn/pki/ca.crt \
      "/etc/openvpn/pki/issued/$ALPHA_ID.crt"
    if openssl verify -crl_check -CAfile /etc/openvpn/pki/ca.crt \
      -CRLfile /etc/openvpn/pki/crl.pem /etc/openvpn/repair/old-alpha.crt; then
      printf "old alpha certificate is not revoked\n" >&2
      exit 1
    fi
    if openssl verify -crl_check -CAfile /etc/openvpn/pki/ca.crt \
      -CRLfile /etc/openvpn/pki/crl.pem /tmp/beta.crt; then
      printf "replacement beta certificate is not revoked\n" >&2
      exit 1
    fi
  '

docker run -d \
  --name "$CONTAINER" \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v "$data_dir:/etc/openvpn" \
  "$IMAGE" >/dev/null
for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" ovpn runtime health >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
docker exec "$CONTAINER" ovpn runtime health
docker stop -t 10 "$CONTAINER" >/dev/null

printf 'schema migration container smoke passed\n'
