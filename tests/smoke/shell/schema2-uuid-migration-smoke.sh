#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT
mkdir -p "$FAKE_BIN"

cat >"$FAKE_BIN/easyrsa" <<'FAKE_EASYRSA'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  build-client-full)
    name="$2"
    if [ "${FAKE_EASYRSA_FAIL_UUID_ISSUE:-false}" = true ] &&
      [[ "$name" =~ ^[0-9a-f-]{36}$ ]]; then
      printf 'injected UUID issuance failure\n' >&2
      exit 1
    fi
    sequence_file="$EASYRSA_PKI/.migration-sequence"
    sequence=10
    [ ! -r "$sequence_file" ] || sequence="$(cat "$sequence_file")"
    sequence=$((sequence + 1))
    printf '%s\n' "$sequence" >"$sequence_file"
    printf 'FAKE CERT %s\n' "$name" >"$EASYRSA_PKI/issued/$name.crt"
    printf 'FAKE KEY %s\n' "$name" >"$EASYRSA_PKI/private/$name.key"
    printf 'V\t30000101000000Z\t\t%02X\tunknown\t/CN=%s\n' "$sequence" "$name" >>"$EASYRSA_PKI/index.txt"
    ;;
  revoke)
    name="$2"
    temporary="$EASYRSA_PKI/index.txt.tmp"
    found=false
    : >"$temporary"
    while IFS= read -r line || [ -n "$line" ]; do
      status="${line%%$'\t'*}"
      subject="${line##*$'\t'}"
      if [ "$status" = V ] && [ "$subject" = "/CN=$name" ]; then
        rest="${line#*$'\t'}"
        expiry="${rest%%$'\t'*}"
        rest="${rest#*$'\t'}"
        rest="${rest#*$'\t'}"
        printf 'R\t%s\t260718000000Z\t%s\n' "$expiry" "$rest" >>"$temporary"
        found=true
      else
        printf '%s\n' "$line" >>"$temporary"
      fi
    done <"$EASYRSA_PKI/index.txt"
    mv "$temporary" "$EASYRSA_PKI/index.txt"
    [ "$found" = true ]
    ;;
  gen-crl)
    printf 'FAKE CRL\n' >"$EASYRSA_PKI/crl.pem"
    ;;
  *)
    printf 'unexpected easyrsa command: %s\n' "$*" >&2
    exit 1
    ;;
esac
FAKE_EASYRSA
chmod +x "$FAKE_BIN/easyrsa"

cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = --version ]; then
  printf 'OpenVPN 2.7.5 migration-test\n'
  exit 0
fi
exit 1
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$LIB_DIR"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_DATA_DIR="$TMP_DIR/data"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_EASYRSA_BIN="$FAKE_BIN/easyrsa"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_MAINTENANCE=true
mkdir -p \
  "$OVPN_DATA_DIR/config" "$OVPN_DATA_DIR/data/leases" "$OVPN_DATA_DIR/meta" \
  "$OVPN_DATA_DIR/pki/issued" "$OVPN_DATA_DIR/pki/private" "$OVPN_DATA_DIR/pki/reqs" \
  "$OVPN_DATA_DIR/clients/active" "$OVPN_DATA_DIR/clients/revoked" \
  "$OVPN_DATA_DIR/ccd" "$OVPN_DATA_DIR/server" "$OVPN_DATA_DIR/secrets" \
  "$OVPN_DATA_DIR/repair/.scripts"

cat >"$OVPN_DATA_DIR/config/project.env" <<'CONFIG'
OVPN_CONFIG_VERSION=2
OVPN_ENDPOINT=vpn.example.test
OVPN_PROTO=udp
OVPN_PORT=1194
OVPN_NETWORK=10.88.0.0/24
OVPN_TOPOLOGY=subnet
OVPN_DYNAMIC_POOL_SIZE=126
OVPN_NAT=false
OVPN_NAT_INTERFACE=auto
OVPN_REDIRECT_GATEWAY=false
OVPN_CLIENT_TO_CLIENT=true
OVPN_DNS=
OVPN_ROUTES=
CONFIG
printf '2\n' >"$OVPN_DATA_DIR/config/schema-version"
cat >"$OVPN_DATA_DIR/meta/client-state.csv" <<'STATE'
# client,state
alpha,active
beta,revoked
gone,deleted
STATE
cat >"$OVPN_DATA_DIR/data/client-ip.csv" <<'DRAFT'
# client,ip
alpha,10.88.0.2
beta,
DRAFT
cat >"$OVPN_DATA_DIR/meta/client-ip.applied.csv" <<'APPLIED'
# client,ip
alpha,10.88.0.3
beta,
APPLIED
: >"$OVPN_DATA_DIR/meta/audit.jsonl"
cat >"$OVPN_DATA_DIR/pki/index.txt" <<'INDEX'
V	30000101000000Z		01	unknown	/CN=openvpn-server
V	30000101000000Z		02	unknown	/CN=alpha
R	30000101000000Z	260101000000Z	03	unknown	/CN=beta
R	30000101000000Z	260101000000Z	04	unknown	/CN=gone
INDEX
printf 'FAKE CA\n' >"$OVPN_DATA_DIR/pki/ca.crt"
printf 'FAKE CA KEY\n' >"$OVPN_DATA_DIR/pki/private/ca.key"
printf 'FAKE SERVER CERT\n' >"$OVPN_DATA_DIR/pki/issued/openvpn-server.crt"
printf 'FAKE SERVER KEY\n' >"$OVPN_DATA_DIR/pki/private/openvpn-server.key"
for name in alpha beta gone; do
  printf 'OLD CERT %s\n' "$name" >"$OVPN_DATA_DIR/pki/issued/$name.crt"
  printf 'OLD KEY %s\n' "$name" >"$OVPN_DATA_DIR/pki/private/$name.key"
done
printf 'TLS KEY\n' >"$OVPN_DATA_DIR/secrets/tls-crypt.key"
printf 'old alpha profile\n' >"$OVPN_DATA_DIR/clients/active/alpha.ovpn"
printf 'old beta profile\n' >"$OVPN_DATA_DIR/clients/revoked/beta.ovpn"
printf 'old ccd\n' >"$OVPN_DATA_DIR/ccd/alpha"
printf '10.88.0.200\n' >"$OVPN_DATA_DIR/data/leases/alpha"
printf 'trusted management bundle\n' >"$OVPN_DATA_DIR/repair/.scripts/sentinel"
chmod 600 \
  "$OVPN_DATA_DIR/config/project.env" "$OVPN_DATA_DIR/config/schema-version" \
  "$OVPN_DATA_DIR/data/client-ip.csv" "$OVPN_DATA_DIR/meta/client-ip.applied.csv" \
  "$OVPN_DATA_DIR/meta/client-state.csv" "$OVPN_DATA_DIR/meta/audit.jsonl" \
  "$OVPN_DATA_DIR/pki/private/ca.key" "$OVPN_DATA_DIR/secrets/tls-crypt.key"

"$OVPN" migrate plan --json >"$TMP_DIR/plan.json"
grep -Fq '"source_schema":2' "$TMP_DIR/plan.json"
grep -Fq '"chain":"2-to-3"' "$TMP_DIR/plan.json"
grep -Fq '"clients":3' "$TMP_DIR/plan.json"
grep -Fq '"blocked":false' "$TMP_DIR/plan.json"
grep -Fq 'must be redistributed' "$TMP_DIR/plan.json"

set +e
"$OVPN" migrate apply >"$TMP_DIR/confirm.out" 2>"$TMP_DIR/confirm.err"
status=$?
set -e
[ "$status" -eq 64 ]
grep -Fq 'requires --yes in non-interactive mode' "$TMP_DIR/confirm.err"
grep -Fqx '2' "$OVPN_DATA_DIR/config/schema-version"

before_failure="$(
  cd "$OVPN_DATA_DIR"
  find ccd clients config data meta pki secrets server -type f -print0 |
    LC_ALL=C sort -z | xargs -0 sha256sum
)"
set +e
FAKE_EASYRSA_FAIL_UUID_ISSUE=true "$OVPN" migrate apply --yes \
  >"$TMP_DIR/issuance-failure.out" 2>"$TMP_DIR/issuance-failure.err"
status=$?
set -e
[ "$status" -eq 1 ]
grep -Fq 'injected UUID issuance failure' "$TMP_DIR/issuance-failure.err"
after_failure="$(
  cd "$OVPN_DATA_DIR"
  find ccd clients config data meta pki secrets server -type f -print0 |
    LC_ALL=C sort -z | xargs -0 sha256sum
)"
[ "$before_failure" = "$after_failure" ]
grep -Fqx '2' "$OVPN_DATA_DIR/config/schema-version"

"$OVPN" migrate apply --yes >"$TMP_DIR/apply.out" 2>"$TMP_DIR/apply.err"
grep -Fqx '3' "$OVPN_DATA_DIR/config/schema-version"
grep -Fqx 'OVPN_CONFIG_VERSION=3' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_TRANSPORT_FAMILY=auto' "$OVPN_DATA_DIR/config/project.env"

alpha_id="$(awk -F, '$2 == "alpha" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
beta_id="$(awk -F, '$2 == "beta" && $3 == "revoked" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
gone_id="$(awk -F, '$2 == "gone" && $3 == "deleted" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
for id in "$alpha_id" "$beta_id" "$gone_id"; do
  [[ "$id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]
done
[ "$alpha_id" != "$beta_id" ] && [ "$alpha_id" != "$gone_id" ] && [ "$beta_id" != "$gone_id" ]
grep -Fqx "$alpha_id,alpha,10.88.0.2" "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx "$alpha_id,alpha,10.88.0.3" "$OVPN_DATA_DIR/meta/client-ip.applied.csv"
grep -Fqx "$beta_id,beta," "$OVPN_DATA_DIR/data/client-ip.csv"
if grep -Fq ',gone,' "$OVPN_DATA_DIR/data/client-ip.csv"; then
  exit 1
fi

grep -Fq "$alpha_id" "$OVPN_DATA_DIR/pki/index.txt"
grep -Fq "$beta_id" "$OVPN_DATA_DIR/pki/index.txt"
if grep -Fq "$gone_id" "$OVPN_DATA_DIR/pki/index.txt"; then
  exit 1
fi
grep -F $'R\t' "$OVPN_DATA_DIR/pki/index.txt" | grep -Fq '/CN=alpha'
grep -F $'V\t' "$OVPN_DATA_DIR/pki/index.txt" | grep -Fq "/CN=$alpha_id"
grep -F $'R\t' "$OVPN_DATA_DIR/pki/index.txt" | grep -Fq "/CN=$beta_id"
test ! -e "$OVPN_DATA_DIR/pki/private/alpha.key"
test ! -e "$OVPN_DATA_DIR/pki/issued/beta.crt"
test -r "$OVPN_DATA_DIR/pki/crl.pem"

grep -Fqx "# ovpn-client-id: $alpha_id" "$OVPN_DATA_DIR/clients/active/alpha.ovpn"
grep -Fqx '# ovpn-client-name: alpha' "$OVPN_DATA_DIR/clients/active/alpha.ovpn"
grep -Fqx "# ovpn-client-id: $beta_id" "$OVPN_DATA_DIR/clients/revoked/beta.ovpn"
grep -Fqx 'ifconfig-push 10.88.0.3 255.255.255.0' "$OVPN_DATA_DIR/ccd/$alpha_id"
test ! -e "$OVPN_DATA_DIR/ccd/alpha"
grep -Fqx '10.88.0.200' "$OVPN_DATA_DIR/data/leases/$alpha_id"
test ! -e "$OVPN_DATA_DIR/data/leases/alpha"
grep -Fqx 'trusted management bundle' "$OVPN_DATA_DIR/repair/.scripts/sentinel"
grep -Fq '"result":"success"' "$OVPN_DATA_DIR"/repair/migrations/reports/*.json
grep -Fq 'redistribute profile:' "$TMP_DIR/apply.out"

"$OVPN" migrate apply --yes

export OVPN_DATA_DIR="$TMP_DIR/schema1"
mkdir -p \
  "$OVPN_DATA_DIR/config" "$OVPN_DATA_DIR/meta" "$OVPN_DATA_DIR/pki/issued" \
  "$OVPN_DATA_DIR/pki/private" "$OVPN_DATA_DIR/pki/reqs" \
  "$OVPN_DATA_DIR/clients/active" "$OVPN_DATA_DIR/clients/revoked" \
  "$OVPN_DATA_DIR/ccd" "$OVPN_DATA_DIR/server" "$OVPN_DATA_DIR/secrets" \
  "$OVPN_DATA_DIR/repair/.scripts"
cat >"$OVPN_DATA_DIR/config/project.env" <<'CONFIG_V1'
OVPN_CONFIG_VERSION=1
OVPN_ENDPOINT=v1.example.test
OVPN_PROTO=tcp
OVPN_PORT=443
OVPN_NETWORK=10.89.0.0/24
OVPN_NAT=true
OVPN_NAT_INTERFACE=auto
OVPN_REDIRECT_GATEWAY=true
OVPN_CLIENT_TO_CLIENT=false
OVPN_DNS=10.89.0.1
OVPN_ROUTES=
CONFIG_V1
printf '1\n' >"$OVPN_DATA_DIR/config/schema-version"
cat >"$OVPN_DATA_DIR/pki/index.txt" <<'INDEX_V1'
V	30000101000000Z		01	unknown	/CN=openvpn-server
V	30000101000000Z		02	unknown	/CN=v1-active
R	30000101000000Z	260101000000Z	03	unknown	/CN=v1-revoked
INDEX_V1
printf 'FAKE CA\n' >"$OVPN_DATA_DIR/pki/ca.crt"
printf 'FAKE CA KEY\n' >"$OVPN_DATA_DIR/pki/private/ca.key"
printf 'FAKE SERVER CERT\n' >"$OVPN_DATA_DIR/pki/issued/openvpn-server.crt"
printf 'FAKE SERVER KEY\n' >"$OVPN_DATA_DIR/pki/private/openvpn-server.key"
for name in v1-active v1-revoked; do
  printf 'OLD CERT %s\n' "$name" >"$OVPN_DATA_DIR/pki/issued/$name.crt"
  printf 'OLD KEY %s\n' "$name" >"$OVPN_DATA_DIR/pki/private/$name.key"
done
printf 'TLS KEY\n' >"$OVPN_DATA_DIR/secrets/tls-crypt.key"
printf 'trusted management bundle\n' >"$OVPN_DATA_DIR/repair/.scripts/sentinel"
chmod 600 \
  "$OVPN_DATA_DIR/config/project.env" "$OVPN_DATA_DIR/config/schema-version" \
  "$OVPN_DATA_DIR/pki/private/ca.key" "$OVPN_DATA_DIR/secrets/tls-crypt.key"

"$OVPN" migrate plan --json >"$TMP_DIR/schema1-plan.json"
grep -Fq '"source_schema":1' "$TMP_DIR/schema1-plan.json"
grep -Fq '"chain":"1-to-2;2-to-3"' "$TMP_DIR/schema1-plan.json"
grep -Fq '"clients":2' "$TMP_DIR/schema1-plan.json"
grep -Fq 'deleted tombstones cannot be recovered' "$TMP_DIR/schema1-plan.json"
"$OVPN" migrate apply --yes >"$TMP_DIR/schema1-apply.out" 2>"$TMP_DIR/schema1-apply.err"
grep -Fqx '3' "$OVPN_DATA_DIR/config/schema-version"
grep -Fqx 'OVPN_CONFIG_VERSION=3' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_TRANSPORT_FAMILY=auto' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_PROTO=tcp' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_REDIRECT_GATEWAY=true' "$OVPN_DATA_DIR/config/project.env"
v1_active_id="$(awk -F, '$2 == "v1-active" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
v1_revoked_id="$(awk -F, '$2 == "v1-revoked" && $3 == "revoked" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
[[ "$v1_active_id" =~ ^[0-9a-f-]{36}$ ]]
[[ "$v1_revoked_id" =~ ^[0-9a-f-]{36}$ ]]
grep -Fqx "$v1_active_id,v1-active," "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx "$v1_revoked_id,v1-revoked," "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx "# ovpn-client-id: $v1_active_id" "$OVPN_DATA_DIR/clients/active/v1-active.ovpn"
grep -Fqx "# ovpn-client-id: $v1_revoked_id" "$OVPN_DATA_DIR/clients/revoked/v1-revoked.ovpn"
grep -Fqx 'trusted management bundle' "$OVPN_DATA_DIR/repair/.scripts/sentinel"

printf 'schema 2 UUID migration smoke passed\n'
