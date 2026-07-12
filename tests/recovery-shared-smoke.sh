#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"

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
  local number

  {
    printf '%s\n' '-----BEGIN OpenVPN Static key V1-----'
    for number in 1 2 3 4 5 6 7 8; do
      printf '%064x\n' "$number"
    done
    printf '%s\n' '-----END OpenVPN Static key V1-----'
  } >"$path"
}

write_profile() {
  local data_dir="$1"
  local path="$2"

  {
    printf '%s\n' '<ca>'
    cat "$data_dir/pki/ca.crt"
    printf '%s\n' '</ca>' '<cert>'
    cat "$data_dir/pki/issued/laptop.crt"
    printf '%s\n' '</cert>' '<key>'
    cat "$data_dir/pki/private/laptop.key"
    printf '%s\n' '</key>' '<tls-crypt>'
    cat "$data_dir/secrets/tls-crypt.key"
    printf '%s\n' '</tls-crypt>'
  } >"$path"
  chmod 600 "$path"
}

make_fixture() {
  local data_dir="$1"
  local fingerprint

  mkdir -p "$data_dir/config" "$data_dir/meta" "$data_dir/server" "$data_dir/pki/private" "$data_dir/pki/issued" "$data_dir/secrets" "$data_dir/clients/active" "$data_dir/ca-db/newcerts"
  printf '%s\n' \
    'OVPN_CONFIG_VERSION=1' \
    'OVPN_ENDPOINT=vpn.example.test' \
    'OVPN_PROTO=udp' \
    'OVPN_PORT=1194' \
    'OVPN_NETWORK=10.88.0.0/24' \
    'OVPN_NAT=true' \
    'OVPN_NAT_INTERFACE=auto' \
    'OVPN_REDIRECT_GATEWAY=false' \
    'OVPN_CLIENT_TO_CLIENT=false' \
    'OVPN_DNS=' \
    'OVPN_ROUTES=' >"$data_dir/config/project.env"
  printf '1\n' >"$data_dir/config/schema-version"
  : >"$data_dir/pki/index.txt"
  printf '01\n' >"$data_dir/pki/serial"
  printf '1000\n' >"$data_dir/ca-db/serial"
  printf '1000\n' >"$data_dir/ca-db/crlnumber"
  : >"$data_dir/ca-db/index.txt"

  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=OpenVPN Container CA' -keyout "$data_dir/pki/private/ca.key" -out "$data_dir/pki/ca.crt" >/dev/null 2>&1
  openssl req -newkey rsa:2048 -nodes -subj '/CN=openvpn-server' -keyout "$data_dir/pki/private/openvpn-server.key" -out "$data_dir/server.csr" >/dev/null 2>&1
  printf '%s\n' 'basicConstraints=CA:FALSE' 'keyUsage=digitalSignature,keyEncipherment' 'extendedKeyUsage=serverAuth' >"$data_dir/server.ext"
  openssl x509 -req -in "$data_dir/server.csr" -CA "$data_dir/pki/ca.crt" -CAkey "$data_dir/pki/private/ca.key" -CAcreateserial -days 1 -out "$data_dir/pki/issued/openvpn-server.crt" -extfile "$data_dir/server.ext" >/dev/null 2>&1
  openssl req -newkey rsa:2048 -nodes -subj '/CN=laptop' -keyout "$data_dir/pki/private/laptop.key" -out "$data_dir/laptop.csr" >/dev/null 2>&1
  printf '%s\n' 'basicConstraints=CA:FALSE' 'keyUsage=digitalSignature,keyEncipherment' 'extendedKeyUsage=clientAuth' >"$data_dir/laptop.ext"
  openssl x509 -req -in "$data_dir/laptop.csr" -CA "$data_dir/pki/ca.crt" -CAkey "$data_dir/pki/private/ca.key" -set_serial 0x01 -days 1 -out "$data_dir/pki/issued/laptop.crt" -extfile "$data_dir/laptop.ext" >/dev/null 2>&1
  printf 'V\t30000101000000Z\t\t01\tunknown\t/CN=laptop\n' >"$data_dir/pki/index.txt"
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

data_dir="$TMP_DIR/openvpn"
make_fixture "$data_dir"
export OVPN_DATA_DIR="$data_dir"

ca_hash="$(sha256sum "$data_dir/pki/ca.crt")"
tls_hash="$(sha256sum "$data_dir/secrets/tls-crypt.key")"
rm "$data_dir/pki/ca.crt" "$data_dir/secrets/tls-crypt.key"
[ "$("$OVPN" state)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair --plan >"$TMP_DIR/plan.out"
grep -Fq '[RECOVER] RECOVER_CA_CERT' "$TMP_DIR/plan.out"
grep -Fq '[RECOVER] RECOVER_TLS_CRYPT_KEY' "$TMP_DIR/plan.out"
"$OVPN" repair >"$TMP_DIR/repair.out" 2>"$TMP_DIR/repair.err"
[ "$("$OVPN" state)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/ca.crt")" = "$ca_hash" ]
[ "$(sha256sum "$data_dir/secrets/tls-crypt.key")" = "$tls_hash" ]
[ "$(stat -c '%a' "$data_dir/pki/ca.crt")" = 644 ]
[ "$(stat -c '%a' "$data_dir/secrets/tls-crypt.key")" = 600 ]
journal="$(rg -l '"result": "success"' "$data_dir/repair/journal" | tail -n 1)"
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
[ "$("$OVPN" state)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/ca.crt")" = "$ca_hash" ]
[ "$(sha256sum "$data_dir/secrets/tls-crypt.key")" = "$tls_hash" ]
client_certificate_hash="$(sha256sum "$data_dir/pki/issued/laptop.crt")"
client_key_hash="$(sha256sum "$data_dir/pki/private/laptop.key")"
rm "$data_dir/pki/issued/laptop.crt"
client_state="$("$OVPN" state)"
if [ "$client_state" != DEGRADED_RECOVERABLE ]; then
  "$OVPN" doctor --json >&2 || true
  echo "expected DEGRADED_RECOVERABLE after client certificate loss, got $client_state" >&2
  exit 1
fi
"$OVPN" repair --plan >"$TMP_DIR/client-cert-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_CERT' "$TMP_DIR/client-cert-plan.out"
"$OVPN" repair >"$TMP_DIR/client-cert-repair.out" 2>"$TMP_DIR/client-cert-repair.err"
[ "$("$OVPN" state)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/issued/laptop.crt")" = "$client_certificate_hash" ]
[ "$(stat -c '%a' "$data_dir/pki/issued/laptop.crt")" = 644 ]

rm "$data_dir/pki/private/laptop.key"
[ "$("$OVPN" state)" = DEGRADED_RECOVERABLE ]
"$OVPN" repair --plan >"$TMP_DIR/client-key-plan.out"
grep -Fq '[RECOVER] RECOVER_CLIENT_KEY' "$TMP_DIR/client-key-plan.out"
"$OVPN" repair >"$TMP_DIR/client-key-repair.out" 2>"$TMP_DIR/client-key-repair.err"
[ "$("$OVPN" state)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/private/laptop.key")" = "$client_key_hash" ]
[ "$(stat -c '%a' "$data_dir/pki/private/laptop.key")" = 600 ]

rm "$data_dir/pki/issued/laptop.crt" "$data_dir/pki/private/laptop.key"
[ "$("$OVPN" state)" = DEGRADED_RECOVERABLE ]
if ! "$OVPN" start >"$TMP_DIR/client-start.out" 2>"$TMP_DIR/client-start.err"; then
  cat "$TMP_DIR/client-start.err" >&2
  exit 1
fi
[ "$("$OVPN" state)" = HEALTHY ]
[ "$(sha256sum "$data_dir/pki/issued/laptop.crt")" = "$client_certificate_hash" ]
[ "$(sha256sum "$data_dir/pki/private/laptop.key")" = "$client_key_hash" ]

rm "$data_dir/clients/active/laptop.ovpn" "$data_dir/clients/active/phone.ovpn" "$data_dir/pki/ca.crt"
[ "$("$OVPN" state)" = CRITICAL ]
set +e
"$OVPN" start >"$TMP_DIR/no-source-start.out" 2>"$TMP_DIR/no-source-start.err"
status=$?
set -e
[ "$status" -eq 78 ]

printf 'shared recovery smoke passed\n'
