#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"

cat >"$FAKE_BIN/easyrsa" <<'FAKE_EASYRSA'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$EASYRSA_PKI"
case "${1:-}" in
  init-pki)
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued" "$EASYRSA_PKI/reqs" "$EASYRSA_PKI/revoked" "$EASYRSA_PKI/certs_by_serial"
    : >"$EASYRSA_PKI/index.txt"
    printf '01\n' >"$EASYRSA_PKI/serial"
    ;;
  build-ca)
    mkdir -p "$EASYRSA_PKI/private"
    printf 'FAKE CA CERT\n' >"$EASYRSA_PKI/ca.crt"
    printf 'FAKE CA KEY\n' >"$EASYRSA_PKI/private/ca.key"
    ;;
  build-server-full)
    name="$2"
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued"
    printf 'FAKE SERVER CERT\n' >"$EASYRSA_PKI/issued/$name.crt"
    printf 'FAKE SERVER KEY\n' >"$EASYRSA_PKI/private/$name.key"
    ;;
  gen-crl)
    printf 'FAKE CRL\n' >"$EASYRSA_PKI/crl.pem"
    ;;
  *)
    echo "unexpected easyrsa command: $*" >&2
    exit 1
    ;;
esac
FAKE_EASYRSA
chmod +x "$FAKE_BIN/easyrsa"

cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  --version)
    printf 'OpenVPN %s test-build\n' "${FAKE_OPENVPN_VERSION:-2.7.5}"
    ;;
  --genkey)
    printf 'FAKE TLS CRYPT KEY\n' >"$3"
    ;;
  *)
    printf 'fake-openvpn %s\n' "$*"
    ;;
esac
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_DIR="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates/openvpn-2.7"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_EASYRSA_BIN="$FAKE_BIN/easyrsa"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"

if [ "$("$OVPN" state)" != EMPTY ]; then
  echo 'fresh data dir should be EMPTY' >&2
  exit 1
fi

set +e
"$OVPN" start >"$TMP_DIR/empty-start.out" 2>"$TMP_DIR/empty-start.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'start unexpectedly succeeded for EMPTY data dir' >&2
  exit 1
fi
if ! grep -q "run 'ovpn init'" "$TMP_DIR/empty-start.err"; then
  echo 'empty start did not explain explicit init requirement' >&2
  exit 1
fi

"$OVPN" init >"$TMP_DIR/init.out" 2>"$TMP_DIR/init.err"
if [ "$("$OVPN" state)" != HEALTHY ]; then
  echo 'initialized data dir should be HEALTHY' >&2
  exit 1
fi

grep -q 'vpn.example.test' "$OVPN_DATA_DIR/config/project.env"
grep -q '^server 10.88.0.0 255.255.255.0$' "$OVPN_DATA_DIR/server/server.conf"
grep -q "$OVPN_DATA_DIR/pki/ca.crt" "$OVPN_DATA_DIR/server/server.conf"
test -f "$OVPN_DATA_DIR/meta/instance.json"
test -f "$OVPN_DATA_DIR/secrets/tls-crypt.key"

"$OVPN" start >"$TMP_DIR/start.out" 2>"$TMP_DIR/start.err"
grep -q -- "--config $OVPN_DATA_DIR/server/server.conf" "$TMP_DIR/start.out"
set +e
FAKE_OPENVPN_VERSION=2.8.0 "$OVPN" start >"$TMP_DIR/unsupported-start.out" 2>"$TMP_DIR/unsupported-start.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'start unexpectedly accepted an unsupported runtime' >&2
  exit 1
fi
grep -q 'outside supported range' "$TMP_DIR/unsupported-start.err"


printf 'init/start smoke passed\n'
