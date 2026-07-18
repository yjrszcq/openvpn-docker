#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"
trap 'rm -rf "$TMP_DIR"' EXIT

cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = --version ]; then
  printf 'OpenVPN 2.7.5 test-build\n'
  exit 0
fi
exit 64
FAKE_OPENVPN

cat >"$FAKE_BIN/ip" <<'FAKE_IP'
#!/usr/bin/env bash
set -euo pipefail
if [ "$*" = '-4 route show default' ]; then
  printf 'default via 172.18.0.1 dev eth0\n'
  exit 0
fi
exit 64
FAKE_IP

cat >"$FAKE_BIN/iptables" <<'FAKE_IPTABLES'
#!/usr/bin/env bash
set -euo pipefail
state="${OVPN_FAKE_IPTABLES_STATE:?}"
touch "$state"
args=("$@")
operation=''
for position in "${!args[@]}"; do
  case "${args[$position]}" in
    -C|-A)
      operation="${args[$position]}"
      unset 'args[$position]'
      ;;
  esac
done
rule="${args[*]}"
case "$operation" in
  -C)
    grep -Fqx -- "$rule" "$state"
    ;;
  -A)
    printf '%s\n' "$rule" >>"$state"
    ;;
  *)
    exit 64
    ;;
esac
FAKE_IPTABLES
chmod +x "$FAKE_BIN/openvpn" "$FAKE_BIN/ip" "$FAKE_BIN/iptables"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_ENDPOINT='vpn.example.test'
export OVPN_NETWORK='10.88.0.0/24'

assert_present() {
  local path="$1"
  local line="$2"

  grep -Fqx -- "$line" "$path"
}

assert_absent() {
  local path="$1"
  local line="$2"

  if grep -Fqx -- "$line" "$path"; then
    echo "unexpected line in $path: $line" >&2
    exit 1
  fi
}

render_case() {
  local nat="$1"
  local redirect="$2"
  local routes="$3"
  local dns="$4"
  local client_to_client="$5"
  local data_dir="$TMP_DIR/render-$nat-$redirect-${routes:+routes}-${dns:+dns}-$client_to_client"
  local server_config="$data_dir/server.conf"

  OVPN_DATA_DIR="$data_dir" \
    OVPN_NAT="$nat" \
    OVPN_NAT_INTERFACE=auto \
    OVPN_REDIRECT_GATEWAY="$redirect" \
    OVPN_ROUTES="$routes" \
    OVPN_DNS="$dns" \
    OVPN_CLIENT_TO_CLIENT="$client_to_client" \
    "$OVPN" config apply
  OVPN_DATA_DIR="$data_dir" "$OVPN" render server --stdout >"$server_config"

  assert_present "$data_dir/config/project.env" "OVPN_NAT=$nat"
  if [ "$client_to_client" = true ]; then
    assert_present "$server_config" 'client-to-client'
  else
    assert_absent "$server_config" 'client-to-client'
  fi
  if [ "$redirect" = true ]; then
    assert_present "$server_config" 'push "redirect-gateway def1"'
  else
    assert_absent "$server_config" 'push "redirect-gateway def1"'
  fi
  if [ -n "$routes" ]; then
    assert_present "$server_config" 'push "route 192.168.50.0 255.255.255.0"'
  else
    assert_absent "$server_config" 'push "route 192.168.50.0 255.255.255.0"'
  fi
  if [ -n "$dns" ]; then
    assert_present "$server_config" 'push "dhcp-option DNS 1.1.1.1"'
    assert_present "$server_config" 'push "dhcp-option DNS 8.8.8.8"'
  else
    assert_absent "$server_config" 'push "dhcp-option DNS 1.1.1.1"'
    assert_absent "$server_config" 'push "dhcp-option DNS 8.8.8.8"'
  fi
}

for nat in true false; do
  for redirect in true false; do
    for routes in '' '192.168.50.0/24'; do
      for dns in '' '1.1.1.1,8.8.8.8'; do
        for client_to_client in true false; do
          render_case "$nat" "$redirect" "$routes" "$dns" "$client_to_client"
        done
      done
    done
  done
done

assert_rejected() {
  local name="$1"
  local expected="$2"
  shift 2
  local data_dir="$TMP_DIR/invalid-$name"
  local status

  set +e
  env   OVPN_DATA_DIR="$data_dir" "$@" "$OVPN" config apply >"$TMP_DIR/$name.out" 2>"$TMP_DIR/$name.err"
  status=$?
  set -e
  [ "$status" -eq 1 ]
  grep -Fq "$expected" "$TMP_DIR/$name.err"
}

assert_rejected dns 'invalid IPv4 address: 1.1.1.999' OVPN_DNS=1.1.1.999
assert_rejected routes 'OVPN_ROUTES must be a comma-separated list of IPv4 CIDRs' OVPN_ROUTES=192.168.50.0/24,
assert_rejected interface 'OVPN_NAT_INTERFACE must be auto or a Linux interface name' OVPN_NAT_INTERFACE='bad iface'

make_project_env() {
  local data_dir="$1"
  local nat="$2"
  local redirect="$3"
  local routes="$4"
  local dns="$5"
  local client_to_client="$6"
  local nat_interface="${7:-auto}"

  mkdir -p "$data_dir/config"
  cat >"$data_dir/config/project.env" <<EOF_PROJECT
OVPN_CONFIG_VERSION=2
OVPN_ENDPOINT=vpn.example.test
OVPN_PROTO=udp
OVPN_TRANSPORT_FAMILY=auto
OVPN_PORT=1194
OVPN_NETWORK=10.88.0.0/24
OVPN_TOPOLOGY=subnet
OVPN_DYNAMIC_POOL_SIZE=126
OVPN_NAT=$nat
OVPN_NAT_INTERFACE=$nat_interface
OVPN_REDIRECT_GATEWAY=$redirect
OVPN_CLIENT_TO_CLIENT=$client_to_client
OVPN_DNS=$dns
OVPN_ROUTES=$routes
EOF_PROJECT
}

configure_runtime_network() {
  local data_dir="$1"
  local forward_file="$2"
  local rules_file="$3"

  OVPN_DATA_DIR="$data_dir" \
    OVPN_IP_BIN="$FAKE_BIN/ip" \
    OVPN_IPTABLES_BIN="$FAKE_BIN/iptables" \
    OVPN_IP_FORWARD_FILE="$forward_file" \
    OVPN_FAKE_IPTABLES_STATE="$rules_file" \
    bash -c '
      set -euo pipefail
      . "$OVPN_LIB_DIR/common.sh"
      . "$OVPN_LIB_DIR/ipam.sh"
      . "$OVPN_LIB_DIR/config.sh"
      . "$OVPN_LIB_DIR/network.sh"
      ovpn_network_configure
    '
}

nat_data="$TMP_DIR/runtime-nat"
nat_forward="$TMP_DIR/nat-forward"
nat_rules="$TMP_DIR/nat-rules"
printf '0\n' >"$nat_forward"
make_project_env "$nat_data" true false '' '' false
configure_runtime_network "$nat_data" "$nat_forward" "$nat_rules"
configure_runtime_network "$nat_data" "$nat_forward" "$nat_rules"
[ "$(cat "$nat_forward")" = 1 ]
[ "$(wc -l <"$nat_rules")" -eq 3 ]
assert_present "$nat_rules" '-w -t nat POSTROUTING -s 10.88.0.0/24 -o eth0 -j MASQUERADE'

routed_data="$TMP_DIR/runtime-routed"
routed_forward="$TMP_DIR/routed-forward"
routed_rules="$TMP_DIR/routed-rules"
printf '0\n' >"$routed_forward"
make_project_env "$routed_data" false false '192.168.50.0/24' '' false
configure_runtime_network "$routed_data" "$routed_forward" "$routed_rules"
[ "$(cat "$routed_forward")" = 1 ]
[ "$(wc -l <"$routed_rules")" -eq 2 ]
if grep -Fq MASQUERADE "$routed_rules"; then
  echo 'routed non-NAT policy unexpectedly installed MASQUERADE' >&2
  exit 1
fi

explicit_data="$TMP_DIR/runtime-explicit"
explicit_forward="$TMP_DIR/explicit-forward"
explicit_rules="$TMP_DIR/explicit-rules"
printf '0\n' >"$explicit_forward"
make_project_env "$explicit_data" true false '' '' false wan0
configure_runtime_network "$explicit_data" "$explicit_forward" "$explicit_rules"
assert_present "$explicit_rules" '-w -t nat POSTROUTING -s 10.88.0.0/24 -o wan0 -j MASQUERADE'

server_only_data="$TMP_DIR/runtime-server-only"
server_only_forward="$TMP_DIR/server-only-forward"
server_only_rules="$TMP_DIR/server-only-rules"
printf '0\n' >"$server_only_forward"
make_project_env "$server_only_data" false false '' '' false
configure_runtime_network "$server_only_data" "$server_only_forward" "$server_only_rules"
[ "$(cat "$server_only_forward")" = 0 ]
test ! -s "$server_only_rules"

printf 'network policy smoke passed (32 render cases, network=10.88.0.0/24)\n'
