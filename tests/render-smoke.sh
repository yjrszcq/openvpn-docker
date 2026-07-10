#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"

FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"
cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = --version ]; then
  printf 'OpenVPN 2.7.5 test-build\n'
  exit 0
fi
exit 64
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"
export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_PROTO="udp"
export OVPN_PORT="1194"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_REDIRECT_GATEWAY="true"
export OVPN_CLIENT_TO_CLIENT="true"
export OVPN_ROUTES="192.168.50.0/24"
export OVPN_DNS="1.1.1.1,8.8.8.8"

"$OVPN" config init
"$OVPN" config print >"$TMP_DIR/project.env.out"
if ! grep -q '^OVPN_NETWORK=10.88.0.0/24$' "$TMP_DIR/project.env.out"; then
  echo 'project config did not preserve validation network' >&2
  exit 1
fi

"$OVPN" render server --stdout >"$TMP_DIR/server.conf"
grep -q '^server 10.88.0.0 255.255.255.0$' "$TMP_DIR/server.conf"
grep -q '^client-to-client$' "$TMP_DIR/server.conf"
grep -q '^push "redirect-gateway def1"$' "$TMP_DIR/server.conf"
grep -q '^push "route 192.168.50.0 255.255.255.0"$' "$TMP_DIR/server.conf"
grep -q '^push "dhcp-option DNS 1.1.1.1"$' "$TMP_DIR/server.conf"

mkdir -p "$OVPN_DATA_DIR/pki/issued" "$OVPN_DATA_DIR/pki/private" "$OVPN_DATA_DIR/secrets"
printf '%s\n' 'TEST CA CERT' >"$OVPN_DATA_DIR/pki/ca.crt"
printf '%s\n' 'TEST CLIENT CERT' >"$OVPN_DATA_DIR/pki/issued/laptop.crt"
printf '%s\n' 'TEST CLIENT KEY' >"$OVPN_DATA_DIR/pki/private/laptop.key"
printf '%s\n' 'TEST TLS CRYPT KEY' >"$OVPN_DATA_DIR/secrets/tls-crypt.key"

"$OVPN" render client laptop --stdout >"$TMP_DIR/laptop.ovpn"
grep -q '^remote vpn.example.test 1194$' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST CA CERT' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST CLIENT CERT' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST CLIENT KEY' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST TLS CRYPT KEY' "$TMP_DIR/laptop.ovpn"

printf 'render smoke passed\n'
