#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
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
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_PROTO="udp"
export OVPN_PORT="1194"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_REDIRECT_GATEWAY="true"
export OVPN_CLIENT_TO_CLIENT="true"
export OVPN_ROUTES="192.168.50.0/24"
export OVPN_DNS="1.1.1.1,8.8.8.8"

"$OVPN" config apply
"$OVPN" config show >"$TMP_DIR/project.env.out"
if ! grep -q '^OVPN_NETWORK=10.88.0.0/24$' "$TMP_DIR/project.env.out"; then
  echo 'project config did not preserve validation network' >&2
  exit 1
fi
grep -Fqx 'OVPN_TRANSPORT_FAMILY=auto' "$TMP_DIR/project.env.out"

"$OVPN" render server --stdout >"$TMP_DIR/server.conf"
grep -q '^server 10.88.0.0 255.255.255.0 nopool$' "$TMP_DIR/server.conf"
grep -q '^ifconfig-pool 10.88.0.129 10.88.0.254$' "$TMP_DIR/server.conf"
grep -q "^client-config-dir $OVPN_DATA_DIR/ccd$" "$TMP_DIR/server.conf"
grep -q '^script-security 2$' "$TMP_DIR/server.conf"
grep -qx 'client-connect /usr/local/lib/openvpn-container/pool-persist-hook.sh' "$TMP_DIR/server.conf"
grep -qx 'client-disconnect /usr/local/lib/openvpn-container/pool-persist-hook.sh' "$TMP_DIR/server.conf"
grep -q "^management $OVPN_RUNTIME_DIR/management.sock unix$" "$TMP_DIR/server.conf"
grep -qx "management-client-user root" "$TMP_DIR/server.conf"
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
grep -q '^proto udp$' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST CA CERT' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST CLIENT CERT' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST CLIENT KEY' "$TMP_DIR/laptop.ovpn"
grep -q 'TEST TLS CRYPT KEY' "$TMP_DIR/laptop.ovpn"

assert_transport_render() {
  local family="$1"
  local proto="$2"
  local endpoint="$3"
  local server_proto="$4"
  local client_proto="$5"

  OVPN_TRANSPORT_FAMILY="$family" OVPN_PROTO="$proto" OVPN_ENDPOINT="$endpoint" "$OVPN" config apply
  "$OVPN" render server --stdout >"$TMP_DIR/server-$family-$proto.conf"
  "$OVPN" render client laptop --stdout >"$TMP_DIR/client-$family-$proto.ovpn"
  grep -Fqx "proto $server_proto" "$TMP_DIR/server-$family-$proto.conf"
  grep -Fqx "proto $client_proto" "$TMP_DIR/client-$family-$proto.ovpn"
  grep -Fqx "remote $endpoint 1194" "$TMP_DIR/client-$family-$proto.ovpn"
}

assert_transport_render auto udp vpn.example.test udp udp
assert_transport_render auto tcp vpn.example.test tcp tcp
assert_transport_render ipv4 udp vpn.example.test udp4 udp4
assert_transport_render ipv4 tcp vpn.example.test tcp4-server tcp4-client
assert_transport_render ipv6 udp 2001:db8::10 udp6 udp6
assert_transport_render ipv6 tcp vpn6.example.test tcp6-server tcp6-client

config_before="$(sha256sum "$OVPN_DATA_DIR/config/project.env")"
if OVPN_TRANSPORT_FAMILY=invalid "$OVPN" config apply >"$TMP_DIR/invalid-family.out" 2>"$TMP_DIR/invalid-family.err"; then
  echo 'invalid transport family unexpectedly applied' >&2
  exit 1
fi
grep -Fq 'OVPN_TRANSPORT_FAMILY must be auto, ipv4, or ipv6' "$TMP_DIR/invalid-family.err"
[ "$config_before" = "$(sha256sum "$OVPN_DATA_DIR/config/project.env")" ]

grep -v '^OVPN_TRANSPORT_FAMILY=' "$OVPN_DATA_DIR/config/project.env" >"$OVPN_DATA_DIR/config/project.env.legacy"
mv "$OVPN_DATA_DIR/config/project.env.legacy" "$OVPN_DATA_DIR/config/project.env"
"$OVPN" config show >"$TMP_DIR/legacy-project.env.out"
grep -Fqx 'OVPN_TRANSPORT_FAMILY=auto' "$TMP_DIR/legacy-project.env.out"

OVPN_DATA_DIR="$TMP_DIR/static" OVPN_DYNAMIC_POOL_SIZE=0 "$OVPN" config apply
OVPN_DATA_DIR="$TMP_DIR/static" "$OVPN" render server --stdout >"$TMP_DIR/static-server.conf"
grep -q '^server 10.88.0.0 255.255.255.0 nopool$' "$TMP_DIR/static-server.conf"
if grep -q '^ifconfig-pool ' "$TMP_DIR/static-server.conf"; then
  echo 'pure static layout unexpectedly rendered a dynamic pool' >&2
  exit 1
fi

printf 'render smoke passed\n'
