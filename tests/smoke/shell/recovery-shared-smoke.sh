#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
CLIENT_ID=11111111-1111-4111-8111-111111111111

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$FAKE_BIN"
cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  --help)
    printf '%s\n' '--tls-crypt key' '--data-ciphers list' '--crl-verify crl' "--topology t: 'subnet'"
    ;;
  --version)
    printf 'OpenVPN 2.7.5 test-build\n'
    ;;
  --config)
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_SERVER_NAME=openvpn-server
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_ENDPOINT=vpn.example.test
export OVPN_NETWORK=10.88.0.0/24
unset OVPN_OPENSSL_BIN

write_tls_crypt_key() {
  local path="$1"
  local offset="${2:-0}"
  local number

  {
    printf '%s\n' '-----BEGIN OpenVPN Static key V1-----'
    for number in 1 2 3 4 5 6 7 8; do
      printf '%064x\n' "$((number + offset))"
    done
    printf '%s\n' '-----END OpenVPN Static key V1-----'
  } >"$path"
}

write_profile_material() {
  local path="$1"
  local ca_path="$2"
  local certificate_path="$3"
  local key_path="$4"
  local tls_crypt_path="$5"

  {
    printf '%s\n' '<ca>'
    cat "$ca_path"
    printf '%s\n' '</ca>' '<cert>'
    cat "$certificate_path"
    printf '%s\n' '</cert>' '<key>'
    cat "$key_path"
    printf '%s\n' '</key>' '<tls-crypt>'
    cat "$tls_crypt_path"
    printf '%s\n' '</tls-crypt>'
  } >"$path"
  chmod 600 "$path"
}

write_profile() {
  local data_dir="$1"
  local path="$2"

  write_profile_material "$path" "$data_dir/pki/ca.crt" "$data_dir/pki/issued/$CLIENT_ID.crt" "$data_dir/pki/private/$CLIENT_ID.key" "$data_dir/secrets/tls-crypt.key"
}

add_profile_identity() {
  local path="$1"
  local name="$2"
  local temporary="$path.identity"

  {
    printf '# ovpn-client-id: %s\n' "$CLIENT_ID"
    printf '# ovpn-client-name: %s\n' "$name"
    cat "$path"
  } >"$temporary"
  mv "$temporary" "$path"
  chmod 600 "$path"
}

make_fixture() {
  local data_dir="$1"
  local fingerprint

  mkdir -p "$data_dir/config" "$data_dir/data" "$data_dir/meta" "$data_dir/server" "$data_dir/pki/private" "$data_dir/pki/issued" "$data_dir/secrets" "$data_dir/clients/active" "$data_dir/ca-db/newcerts"
  printf '%s\n' \
    'OVPN_CONFIG_VERSION=3' \
    'OVPN_ENDPOINT=vpn.example.test' \
    'OVPN_PROTO=udp' \
    'OVPN_PORT=1194' \
    'OVPN_NETWORK=10.88.0.0/24' \
    'OVPN_TOPOLOGY=subnet' \
    'OVPN_DYNAMIC_POOL_SIZE=126' \
    'OVPN_NAT=false' \
    'OVPN_NAT_INTERFACE=auto' \
    'OVPN_REDIRECT_GATEWAY=false' \
    'OVPN_CLIENT_TO_CLIENT=false' \
    'OVPN_DNS=' \
    'OVPN_ROUTES=' >"$data_dir/config/project.env"
  printf '3\n' >"$data_dir/config/schema-version"
  printf '%s\n' '# id,name,ip' '11111111-1111-4111-8111-111111111111,laptop,' >"$data_dir/data/client-ip.csv"
  cp "$data_dir/data/client-ip.csv" "$data_dir/meta/client-ip.applied.csv"
  printf '%s\n' '# id,name,state' '11111111-1111-4111-8111-111111111111,laptop,active' >"$data_dir/meta/client-state.csv"
  : >"$data_dir/meta/audit.jsonl"
  chmod 600 "$data_dir/data/client-ip.csv" "$data_dir/meta/client-ip.applied.csv" "$data_dir/meta/client-state.csv" "$data_dir/meta/audit.jsonl"
  : >"$data_dir/pki/index.txt"
  printf '01\n' >"$data_dir/pki/serial"
  printf '1000\n' >"$data_dir/ca-db/serial"
  printf '1000\n' >"$data_dir/ca-db/crlnumber"
  : >"$data_dir/ca-db/index.txt"

  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=OpenVPN Container CA' -keyout "$data_dir/pki/private/ca.key" -out "$data_dir/pki/ca.crt" >/dev/null 2>&1
  openssl req -newkey rsa:2048 -nodes -subj '/CN=openvpn-server' -keyout "$data_dir/pki/private/openvpn-server.key" -out "$data_dir/server.csr" >/dev/null 2>&1
  printf '%s\n' 'basicConstraints=CA:FALSE' 'keyUsage=digitalSignature,keyEncipherment' 'extendedKeyUsage=serverAuth' >"$data_dir/server.ext"
  openssl x509 -req -in "$data_dir/server.csr" -CA "$data_dir/pki/ca.crt" -CAkey "$data_dir/pki/private/ca.key" -CAcreateserial -days 1 -out "$data_dir/pki/issued/openvpn-server.crt" -extfile "$data_dir/server.ext" >/dev/null 2>&1
  openssl req -newkey rsa:2048 -nodes -subj "/CN=$CLIENT_ID" -keyout "$data_dir/pki/private/$CLIENT_ID.key" -out "$data_dir/laptop.csr" >/dev/null 2>&1
  printf '%s\n' 'basicConstraints=CA:FALSE' 'keyUsage=digitalSignature,keyEncipherment' 'extendedKeyUsage=clientAuth' >"$data_dir/laptop.ext"
  openssl x509 -req -in "$data_dir/laptop.csr" -CA "$data_dir/pki/ca.crt" -CAkey "$data_dir/pki/private/ca.key" -set_serial 0x01 -days 1 -out "$data_dir/pki/issued/$CLIENT_ID.crt" -extfile "$data_dir/laptop.ext" >/dev/null 2>&1
  printf 'V\t30000101000000Z\t\t01\tunknown\t/CN=%s\n' "$CLIENT_ID" >"$data_dir/pki/index.txt"
  cat >"$data_dir/crl.cnf" <<CRL_CONFIG
[ ca ]
default_ca = CA_default
[ CA_default ]
database = $data_dir/ca-db/index.txt
new_certs_dir = $data_dir/ca-db/newcerts
certificate = $data_dir/pki/ca.crt
private_key = $data_dir/pki/private/ca.key
serial = $data_dir/ca-db/serial
crlnumber = $data_dir/ca-db/crlnumber
default_md = sha256
default_crl_days = 1
policy = policy_any
[ policy_any ]
commonName = supplied
CRL_CONFIG
  openssl ca -config "$data_dir/crl.cnf" -gencrl -out "$data_dir/pki/crl.pem" >/dev/null 2>&1
  write_tls_crypt_key "$data_dir/secrets/tls-crypt.key"
  : >"$data_dir/server/server.conf"
  write_profile "$data_dir" "$data_dir/clients/active/laptop.ovpn"
  write_profile "$data_dir" "$data_dir/clients/active/phone.ovpn"
  fingerprint="$(openssl x509 -in "$data_dir/pki/ca.crt" -noout -fingerprint -sha256)"
  printf '{\n  "ca_fingerprint_sha256": "%s"\n}\n' "${fingerprint#*=}" >"$data_dir/meta/instance.json"
}

snapshot_data_dir() (
  cd "$1"
  find . -type f ! -name .ovpn-data.lock ! -name .ovpn-runtime.lock -printf '%P\n' | LC_ALL=C sort | while IFS= read -r path; do
    [ -n "$path" ] || continue
    printf '%s %s ' "$(stat -c '%a' "$path")" "$path"
    sha256sum "$path"
  done
)

assert_critical_recovery() {
  local fixture="$1"
  local issue="$2"
  local label="$3"
  local before after status

  export OVPN_DATA_DIR="$fixture"
  [ "$("$OVPN" state show)" = CRITICAL ]
  before="$(snapshot_data_dir "$fixture")"

  set +e
  "$OVPN" state doctor --json >"$TMP_DIR/$label-doctor.out" 2>"$TMP_DIR/$label-doctor.err"
  status=$?
  set -e
  [ "$status" -eq 78 ]
  grep -Fq "$issue" "$TMP_DIR/$label-doctor.out"
  after="$(snapshot_data_dir "$fixture")"
  [ "$before" = "$after" ]

  set +e
  "$OVPN" repair plan >"$TMP_DIR/$label-plan.out" 2>"$TMP_DIR/$label-plan.err"
  status=$?
  set -e
  [ "$status" -eq 78 ]
  grep -Fq "[BLOCKED] $issue" "$TMP_DIR/$label-plan.out"
  after="$(snapshot_data_dir "$fixture")"
  [ "$before" = "$after" ]

  set +e
  "$OVPN" start >"$TMP_DIR/$label-start.out" 2>"$TMP_DIR/$label-start.err"
  status=$?
  set -e
  [ "$status" -eq 78 ]
  after="$(snapshot_data_dir "$fixture")"
  [ "$before" = "$after" ]
}

data_dir="$TMP_DIR/openvpn"
make_fixture "$data_dir"
export OVPN_DATA_DIR="$data_dir"

ca_hash="$(sha256sum "$data_dir/pki/ca.crt")"
tls_hash="$(sha256sum "$data_dir/secrets/tls-crypt.key")"
rm "$data_dir/pki/ca.crt" "$data_dir/secrets/tls-crypt.key"
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair plan >"$TMP_DIR/plan.out"
grep -Fq '[RECOVER] RECOVER_CA_CERT' "$TMP_DIR/plan.out"
grep -Fq '[RECOVER] RECOVER_TLS_CRYPT_KEY' "$TMP_DIR/plan.out"
"$OVPN" repair apply >"$TMP_DIR/repair.out" 2>"$TMP_DIR/repair.err"
[ "$("$OVPN" state show)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/ca.crt")" = "$ca_hash" ]
[ "$(sha256sum "$data_dir/secrets/tls-crypt.key")" = "$tls_hash" ]
[ "$(stat -c '%a' "$data_dir/pki/ca.crt")" = 644 ]
[ "$(stat -c '%a' "$data_dir/secrets/tls-crypt.key")" = 600 ]
journal="$(grep -rl -- '"result": "success"' "$data_dir/repair/journal" | tail -n 1)"
[ -n "$journal" ]
grep -Fq 'RECOVER_CA_CERT' "$journal"
grep -Fq 'RECOVER_TLS_CRYPT_KEY' "$journal"
if grep -Fq 'BEGIN OpenVPN Static key' "$journal"; then
  echo 'recovery journal contains tls-crypt material' >&2
  exit 1
fi

rm "$data_dir/pki/ca.crt" "$data_dir/secrets/tls-crypt.key"
if ! "$OVPN" start >"$TMP_DIR/start.out" 2>"$TMP_DIR/start.err"; then
  cat "$TMP_DIR/start.err" >&2
  exit 1
fi
[ "$("$OVPN" state show)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/ca.crt")" = "$ca_hash" ]
[ "$(sha256sum "$data_dir/secrets/tls-crypt.key")" = "$tls_hash" ]
client_certificate_hash="$(sha256sum "$data_dir/pki/issued/$CLIENT_ID.crt")"
client_key_hash="$(sha256sum "$data_dir/pki/private/$CLIENT_ID.key")"
rm "$data_dir/pki/issued/$CLIENT_ID.crt"
client_state="$("$OVPN" state show)"
if [ "$client_state" != DEGRADED_RECOVERABLE ]; then
  "$OVPN" state doctor --json >&2 || true
  echo "expected DEGRADED_RECOVERABLE after client certificate loss, got $client_state" >&2
  exit 1
fi
"$OVPN" repair plan >"$TMP_DIR/client-cert-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_CERT' "$TMP_DIR/client-cert-plan.out"
"$OVPN" repair apply >"$TMP_DIR/client-cert-repair.out" 2>"$TMP_DIR/client-cert-repair.err"
[ "$("$OVPN" state show)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/issued/$CLIENT_ID.crt")" = "$client_certificate_hash" ]
[ "$(stat -c '%a' "$data_dir/pki/issued/$CLIENT_ID.crt")" = 644 ]

rm "$data_dir/pki/private/$CLIENT_ID.key"
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair plan >"$TMP_DIR/client-key-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_KEY' "$TMP_DIR/client-key-plan.out"
"$OVPN" repair apply >"$TMP_DIR/client-key-repair.out" 2>"$TMP_DIR/client-key-repair.err"
[ "$("$OVPN" state show)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/private/$CLIENT_ID.key")" = "$client_key_hash" ]
[ "$(stat -c '%a' "$data_dir/pki/private/$CLIENT_ID.key")" = 600 ]

rm "$data_dir/pki/issued/$CLIENT_ID.crt" "$data_dir/pki/private/$CLIENT_ID.key"
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
if ! "$OVPN" start >"$TMP_DIR/client-start.out" 2>"$TMP_DIR/client-start.err"; then
  cat "$TMP_DIR/client-start.err" >&2
  exit 1
fi
[ "$("$OVPN" state show)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/issued/$CLIENT_ID.crt")" = "$client_certificate_hash" ]
[ "$(sha256sum "$data_dir/pki/private/$CLIENT_ID.key")" = "$client_key_hash" ]

rm "$data_dir/clients/active/laptop.ovpn" "$data_dir/clients/active/phone.ovpn" "$data_dir/pki/ca.crt"
[ "$("$OVPN" state show)" = CRITICAL ]
set +e
"$OVPN" start >"$TMP_DIR/no-source-start.out" 2>"$TMP_DIR/no-source-start.err"
status=$?
set -e
[ "$status" -eq 78 ]


conflicting_ca_data="$TMP_DIR/conflicting-ca"
make_fixture "$conflicting_ca_data"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Conflicting Recovery CA' -keyout "$TMP_DIR/conflicting-ca.key" -out "$TMP_DIR/conflicting-ca.crt" >/dev/null 2>&1
write_profile_material "$conflicting_ca_data/clients/active/phone.ovpn" "$TMP_DIR/conflicting-ca.crt" "$conflicting_ca_data/pki/issued/$CLIENT_ID.crt" "$conflicting_ca_data/pki/private/$CLIENT_ID.key" "$conflicting_ca_data/secrets/tls-crypt.key"
rm "$conflicting_ca_data/pki/ca.crt"
assert_critical_recovery "$conflicting_ca_data" CRITICAL_RECOVERY_CONFLICT conflicting-ca

conflicting_tls_data="$TMP_DIR/conflicting-tls"
make_fixture "$conflicting_tls_data"
write_tls_crypt_key "$TMP_DIR/conflicting-tls.key" 8
write_profile_material "$conflicting_tls_data/clients/active/phone.ovpn" "$conflicting_tls_data/pki/ca.crt" "$conflicting_tls_data/pki/issued/$CLIENT_ID.crt" "$conflicting_tls_data/pki/private/$CLIENT_ID.key" "$TMP_DIR/conflicting-tls.key"
rm "$conflicting_tls_data/secrets/tls-crypt.key"
assert_critical_recovery "$conflicting_tls_data" CRITICAL_RECOVERY_CONFLICT conflicting-tls

malformed_ca_data="$TMP_DIR/malformed-ca"
make_fixture "$malformed_ca_data"
printf '%s\n' '<ca>' 'not-a-certificate' >>"$malformed_ca_data/clients/active/phone.ovpn"
rm "$malformed_ca_data/pki/ca.crt"
assert_critical_recovery "$malformed_ca_data" CA_CERT_RECOVERY_INVALID malformed-ca

non_equivalent_ca_data="$TMP_DIR/non-equivalent-ca"
make_fixture "$non_equivalent_ca_data"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=Non-equivalent Recovery CA' -keyout "$TMP_DIR/non-equivalent-ca.key" -out "$TMP_DIR/non-equivalent-ca.crt" >/dev/null 2>&1
write_profile_material "$non_equivalent_ca_data/clients/active/laptop.ovpn" "$TMP_DIR/non-equivalent-ca.crt" "$non_equivalent_ca_data/pki/issued/$CLIENT_ID.crt" "$non_equivalent_ca_data/pki/private/$CLIENT_ID.key" "$non_equivalent_ca_data/secrets/tls-crypt.key"
write_profile_material "$non_equivalent_ca_data/clients/active/phone.ovpn" "$TMP_DIR/non-equivalent-ca.crt" "$non_equivalent_ca_data/pki/issued/$CLIENT_ID.crt" "$non_equivalent_ca_data/pki/private/$CLIENT_ID.key" "$non_equivalent_ca_data/secrets/tls-crypt.key"
rm "$non_equivalent_ca_data/pki/ca.crt"
assert_critical_recovery "$non_equivalent_ca_data" CA_CERT_RECOVERY_INVALID non-equivalent-ca

mismatched_client_data="$TMP_DIR/mismatched-client"
make_fixture "$mismatched_client_data"
openssl genpkey -algorithm RSA -out "$TMP_DIR/mismatched-client.key" >/dev/null 2>&1
write_profile_material "$mismatched_client_data/clients/active/laptop.ovpn" "$mismatched_client_data/pki/ca.crt" "$mismatched_client_data/pki/issued/$CLIENT_ID.crt" "$TMP_DIR/mismatched-client.key" "$mismatched_client_data/secrets/tls-crypt.key"
rm "$mismatched_client_data/pki/private/$CLIENT_ID.key"
assert_critical_recovery "$mismatched_client_data" CLIENT_IDENTITY_RECOVERY_INVALID_laptop mismatched-client

identity_data="$TMP_DIR/identity"
make_fixture "$identity_data"
rm "$identity_data/clients/active/phone.ovpn"
add_profile_identity "$identity_data/clients/active/laptop.ovpn" laptop
rm "$identity_data/meta/client-state.csv"
export OVPN_DATA_DIR="$identity_data"
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair plan >"$TMP_DIR/identity-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_IDENTITY_REGISTRY' "$TMP_DIR/identity-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_IP_DRAFT' "$TMP_DIR/identity-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_IP_APPLIED' "$TMP_DIR/identity-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_PROFILES' "$TMP_DIR/identity-plan.out"
identity_draft_before="$(sha256sum "$identity_data/data/client-ip.csv")"
identity_applied_before="$(sha256sum "$identity_data/meta/client-ip.applied.csv")"
identity_profile_before="$(sha256sum "$identity_data/clients/active/laptop.ovpn")"
if OVPN_REPAIR_FAIL_AFTER_INSTALL=RECOVER_CLIENT_IP_DRAFT \
  "$OVPN" repair apply >"$TMP_DIR/identity-failed-repair.out" 2>"$TMP_DIR/identity-failed-repair.err"; then
  echo 'injected identity registry repair failure unexpectedly succeeded' >&2
  exit 1
fi
[ ! -e "$identity_data/meta/client-state.csv" ]
[ "$(sha256sum "$identity_data/data/client-ip.csv")" = "$identity_draft_before" ]
[ "$(sha256sum "$identity_data/meta/client-ip.applied.csv")" = "$identity_applied_before" ]
[ "$(sha256sum "$identity_data/clients/active/laptop.ovpn")" = "$identity_profile_before" ]
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair apply >"$TMP_DIR/identity-repair.out" 2>"$TMP_DIR/identity-repair.err"
[ "$("$OVPN" state show)" = HEALTHY ]
grep -Fqx "$CLIENT_ID,laptop,active" "$identity_data/meta/client-state.csv"
grep -Fqx "$CLIENT_ID,laptop," "$identity_data/data/client-ip.csv"
grep -Fqx "$CLIENT_ID,laptop," "$identity_data/meta/client-ip.applied.csv"
grep -Fqx "# ovpn-client-id: $CLIENT_ID" "$identity_data/clients/active/laptop.ovpn"
grep -Fqx '# ovpn-client-name: laptop' "$identity_data/clients/active/laptop.ovpn"

conflicting_identity_data="$TMP_DIR/conflicting-identity"
make_fixture "$conflicting_identity_data"
rm "$conflicting_identity_data/clients/active/phone.ovpn"
add_profile_identity "$conflicting_identity_data/clients/active/laptop.ovpn" workstation
rm "$conflicting_identity_data/meta/client-state.csv"
assert_critical_recovery "$conflicting_identity_data" CLIENT_IDENTITY_RECOVERY_CONFLICT conflicting-identity

audit_identity_data="$TMP_DIR/audit-identity"
make_fixture "$audit_identity_data"
printf '{"timestamp":"2026-07-18T00:00:00Z","operation":"rename","result":"applied","client_id":"%s","old_name":"laptop","new_name":"workstation"}\n' \
  "$CLIENT_ID" >"$audit_identity_data/meta/audit.jsonl"
chmod 600 "$audit_identity_data/meta/audit.jsonl"
rm "$audit_identity_data/meta/client-state.csv" \
  "$audit_identity_data/data/client-ip.csv" \
  "$audit_identity_data/meta/client-ip.applied.csv"
rm -rf "$audit_identity_data/clients"
export OVPN_DATA_DIR="$audit_identity_data"
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair apply >"$TMP_DIR/audit-identity-repair.out" 2>"$TMP_DIR/audit-identity-repair.err"
[ "$("$OVPN" state show)" = HEALTHY ]
grep -Fqx "$CLIENT_ID,workstation,active" "$audit_identity_data/meta/client-state.csv"
grep -Fqx "# ovpn-client-name: workstation" "$audit_identity_data/clients/active/workstation.ovpn"

uuid_only_data="$TMP_DIR/uuid-only"
make_fixture "$uuid_only_data"
rm "$uuid_only_data/meta/client-state.csv" \
  "$uuid_only_data/data/client-ip.csv" \
  "$uuid_only_data/meta/client-ip.applied.csv"
rm -rf "$uuid_only_data/clients"
export OVPN_DATA_DIR="$uuid_only_data"
[ "$("$OVPN" state show)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair apply >"$TMP_DIR/uuid-only-repair.out" 2>"$TMP_DIR/uuid-only-repair.err"
[ "$("$OVPN" state show)" = HEALTHY ]
temporary_name="client-${CLIENT_ID//-/}"
grep -Fqx "$CLIENT_ID,$temporary_name,active" "$uuid_only_data/meta/client-state.csv"
grep -Fqx "$CLIENT_ID,$temporary_name," "$uuid_only_data/data/client-ip.csv"
grep -Fqx "$CLIENT_ID,$temporary_name," "$uuid_only_data/meta/client-ip.applied.csv"
grep -Fqx "# ovpn-client-id: $CLIENT_ID" "$uuid_only_data/clients/active/$temporary_name.ovpn"
grep -Fqx "# ovpn-client-name: $temporary_name" "$uuid_only_data/clients/active/$temporary_name.ovpn"

printf 'recovery smoke passed\n'
