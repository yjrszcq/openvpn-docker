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
  build-client-full)
    name="$2"
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued"
    printf 'FAKE CLIENT CERT %s\n' "$name" >"$EASYRSA_PKI/issued/$name.crt"
    printf 'FAKE CLIENT KEY %s\n' "$name" >"$EASYRSA_PKI/private/$name.key"
    printf 'V\t30000101000000Z\t\t01\tunknown\t/CN=%s\n' "$name" >>"$EASYRSA_PKI/index.txt"
    ;;
  revoke)
    name="$2"
    tmp="$EASYRSA_PKI/index.txt.tmp"
    found=0
    while IFS= read -r line || [ -n "$line" ]; do
      status="${line%%$'\t'*}"
      subject="${line##*$'\t'}"
      if [ "$status" = V ] && [ "$subject" = "/CN=$name" ]; then
        printf 'R\t30000101000000Z\t260101000000Z\t01\tunknown\t/CN=%s\n' "$name" >>"$tmp"
        found=1
      else
        printf '%s\n' "$line" >>"$tmp"
      fi
    done <"$EASYRSA_PKI/index.txt"
    mv "$tmp" "$EASYRSA_PKI/index.txt"
    [ "$found" -eq 1 ]
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
    printf 'OpenVPN 2.7.5 test-build\n'
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
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_EASYRSA_BIN="$FAKE_BIN/easyrsa"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"

"$OVPN" init >/tmp/ovpn-client-init.out 2>/tmp/ovpn-client-init.err
"$OVPN" add-client laptop >/tmp/ovpn-add-client.out 2>/tmp/ovpn-add-client.err

grep -q '^laptop active$' <("$OVPN" list-clients)
test -f "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

"$OVPN" export-client laptop >"$TMP_DIR/laptop.ovpn" 2>"$TMP_DIR/export.err"
test ! -s "$TMP_DIR/export.err"
grep -q '^remote vpn.example.test 1194$' "$TMP_DIR/laptop.ovpn"
grep -q 'FAKE CLIENT CERT laptop' "$TMP_DIR/laptop.ovpn"
grep -q 'FAKE CLIENT KEY laptop' "$TMP_DIR/laptop.ovpn"

set +e
"$OVPN" add-client laptop >"$TMP_DIR/duplicate.out" 2>"$TMP_DIR/duplicate.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'duplicate add-client unexpectedly succeeded' >&2
  exit 1
fi

grep -q 'already exists' "$TMP_DIR/duplicate.err"

"$OVPN" revoke-client laptop >"$TMP_DIR/revoke.out" 2>"$TMP_DIR/revoke.err"
grep -q '^laptop revoked$' <("$OVPN" list-clients)
test -f "$OVPN_DATA_DIR/clients/revoked/laptop.ovpn"
test ! -e "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

set +e
"$OVPN" export-client laptop >"$TMP_DIR/revoked-export.out" 2>"$TMP_DIR/revoked-export.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'revoked client export unexpectedly succeeded' >&2
  exit 1
fi

grep -q 'is revoked' "$TMP_DIR/revoked-export.err"

printf 'client lifecycle smoke passed\n'
