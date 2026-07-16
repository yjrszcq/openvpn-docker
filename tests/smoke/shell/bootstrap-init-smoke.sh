#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"
trap 'rm -rf "$TMP_DIR"' EXIT

cat >"$FAKE_BIN/easyrsa" <<'FAKE_EASYRSA'
#!/usr/bin/env bash
set -euo pipefail
if [ "${FAKE_EASYRSA_FAIL:-}" = "${1:-}" ]; then
  exit 77
fi
mkdir -p "$EASYRSA_PKI"
case "${1:-}" in
  init-pki)
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued" "$EASYRSA_PKI/reqs" "$EASYRSA_PKI/revoked" "$EASYRSA_PKI/certs_by_serial"
    : >"$EASYRSA_PKI/index.txt"
    printf '01\n' >"$EASYRSA_PKI/serial"
    ;;
  build-ca)
    printf 'FAKE CA CERT\n' >"$EASYRSA_PKI/ca.crt"
    printf 'FAKE CA KEY\n' >"$EASYRSA_PKI/private/ca.key"
    ;;
  build-server-full)
    name="$2"
    printf 'FAKE SERVER CERT\n' >"$EASYRSA_PKI/issued/$name.crt"
    printf 'FAKE SERVER KEY\n' >"$EASYRSA_PKI/private/$name.key"
    ;;
  gen-crl)
    printf 'FAKE CRL\n' >"$EASYRSA_PKI/crl.pem"
    ;;
  *)
    exit 64
    ;;
esac
FAKE_EASYRSA
chmod +x "$FAKE_BIN/easyrsa"

cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  --version)
    printf 'OpenVPN 2.7.5 test-build\n'
    ;;
  --genkey)
    printf 'FAKE TLS CRYPT KEY\n' >"$3"
    ;;
  *)
    exit 64
    ;;
esac
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_EASYRSA_BIN="$FAKE_BIN/easyrsa"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"
export OVPN_NETWORK=10.88.0.0/24
export OVPN_DATA_DIR="$TMP_DIR/valid"
export OVPN_ENDPOINT=vpn.example.test

"$OVPN" init

grep -Fqx 'OVPN_ENDPOINT=vpn.example.test' "$OVPN_DATA_DIR/config/project.env"
grep -Fqx 'OVPN_NETWORK=10.88.0.0/24' "$OVPN_DATA_DIR/config/project.env"
OVPN_ENDPOINT=changed.example.test "$OVPN" config show >"$TMP_DIR/config.out"
grep -Fqx 'OVPN_ENDPOINT=vpn.example.test' "$TMP_DIR/config.out"

valid_ipv6_endpoints=('::' '::1' '::ffff:192.0.2.1')
for index in "${!valid_ipv6_endpoints[@]}"; do
  endpoint="${valid_ipv6_endpoints[index]}"
  data_dir="$TMP_DIR/valid-ipv6-endpoint-$index"
  OVPN_DATA_DIR="$data_dir" OVPN_ENDPOINT="$endpoint" "$OVPN" config apply
  grep -Fqx "OVPN_ENDPOINT=$endpoint" "$data_dir/config/project.env"
done

invalid_ipv6_endpoints=(':1' '[::1]' '[2001:db8::1]')
for index in "${!invalid_ipv6_endpoints[@]}"; do
  endpoint="${invalid_ipv6_endpoints[index]}"
  data_dir="$TMP_DIR/invalid-ipv6-endpoint-$index"
  if OVPN_DATA_DIR="$data_dir" OVPN_ENDPOINT="$endpoint" "$OVPN" config apply >"$TMP_DIR/invalid-ipv6-$index.out" 2>"$TMP_DIR/invalid-ipv6-$index.err"; then
    echo "invalid IPv6 endpoint unexpectedly applied: $endpoint" >&2
    exit 1
  fi
  grep -Fq 'OVPN_ENDPOINT must be a hostname or IP address' "$TMP_DIR/invalid-ipv6-$index.err"
done

export OVPN_DATA_DIR="$TMP_DIR/invalid-endpoint"
set +e
OVPN_ENDPOINT='bad endpoint' "$OVPN" init >"$TMP_DIR/invalid.out" 2>"$TMP_DIR/invalid.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'invalid bootstrap endpoint unexpectedly initialized' >&2
  exit 1
fi
grep -Fq 'OVPN_ENDPOINT must be a hostname or IP address' "$TMP_DIR/invalid.err"
if [ "$("$OVPN" state show)" != EMPTY ]; then
  echo 'invalid bootstrap input left non-empty data' >&2
  exit 1
fi

action=build-ca
export OVPN_DATA_DIR="$TMP_DIR/failed-pki"
set +e
FAKE_EASYRSA_FAIL="$action" OVPN_ENDPOINT=vpn.example.test "$OVPN" init >"$TMP_DIR/failed.out" 2>"$TMP_DIR/failed.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'failed PKI initialization unexpectedly succeeded' >&2
  exit 1
fi
if [ "$("$OVPN" state show)" != EMPTY ]; then
  echo 'failed PKI initialization left non-empty data' >&2
  exit 1
fi
if compgen -G "$OVPN_DATA_DIR/.staging-init-*" >/dev/null || [ -e "$OVPN_DATA_DIR/.init-transaction" ]; then
  echo 'failed PKI initialization left transaction artifacts' >&2
  exit 1
fi

export OVPN_DATA_DIR="$TMP_DIR/interrupted-commit"
mkdir -p "$OVPN_DATA_DIR"
: >"$OVPN_DATA_DIR/.init-transaction"
if [ "$("$OVPN" state show)" != CRITICAL ]; then
  echo 'interrupted initialization should be CRITICAL' >&2
  exit 1
fi

printf 'bootstrap init smoke passed\n'
