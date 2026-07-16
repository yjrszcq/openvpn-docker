#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_E2E_IMAGE:-szcq/openvpn-server:e2e}"
NETWORK="10.88.0.0/24"
PORT="${OVPN_PORT:-1194}"
CONNECT_TIMEOUT="${OVPN_E2E_CONNECT_TIMEOUT:-20s}"
WAIT_SECONDS="${OVPN_E2E_WAIT_SECONDS:-30}"
REQUIRED="${OVPN_E2E_REQUIRED:-0}"
SKIP_BUILD="${OVPN_E2E_SKIP_BUILD:-0}"
RUN_ID="ovpn-e2e-$$-$(date +%s)"
CONTROL_RUNTIME_DIR="/tmp/openvpn-container-$RUN_ID"
POLICY_NAT=true
POLICY_NAT_INTERFACE=auto
POLICY_REDIRECT_GATEWAY=false
POLICY_CLIENT_TO_CLIENT=false
POLICY_DNS=''
POLICY_ROUTES=''
transport_family=auto

containers=()
networks=()
WORK_DIR=""

skip_or_fail() {
  local reason="$1"
  if [ "$REQUIRED" = 1 ]; then
    echo "e2e container smoke failed: $reason" >&2
    exit 1
  fi
  echo "e2e container smoke skipped: $reason"
  exit 0
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || skip_or_fail "missing command: $1"
}

cleanup() {
  local item
  for item in "${containers[@]}"; do
    docker rm -f "$item" >/dev/null 2>&1 || true
  done
  for item in "${networks[@]}"; do
    docker network rm "$item" >/dev/null 2>&1 || true
  done
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint /bin/sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
  fi
}
trap cleanup EXIT

need_command docker
if ! docker info >/dev/null 2>&1; then
  skip_or_fail "Docker daemon is not accessible"
fi

if [ ! -c /dev/net/tun ]; then
  skip_or_fail "host /dev/net/tun is not available"
fi

if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-e2e.XXXXXX")"

run_control() {
  local data_dir="$1"
  local endpoint="$2"
  shift 2
  docker run --rm \
    -e "OVPN_RUNTIME_DIR=$CONTROL_RUNTIME_DIR" \
    -e "OVPN_ENDPOINT=$endpoint" \
    -e "OVPN_NETWORK=$NETWORK" \
    -e "OVPN_PORT=$PORT" \
    -e "OVPN_PROTO=$proto" \
    -e "OVPN_TRANSPORT_FAMILY=$transport_family" \
    -e "OVPN_NAT=$POLICY_NAT" \
    -e "OVPN_NAT_INTERFACE=$POLICY_NAT_INTERFACE" \
    -e "OVPN_REDIRECT_GATEWAY=$POLICY_REDIRECT_GATEWAY" \
    -e "OVPN_CLIENT_TO_CLIENT=$POLICY_CLIENT_TO_CLIENT" \
    -e "OVPN_DNS=$POLICY_DNS" \
    -e "OVPN_ROUTES=$POLICY_ROUTES" \
    -v "$data_dir:/etc/openvpn" \
    "$IMAGE" \
    "$@"
}

data_grep() {
  local data_dir="$1"
  local pattern="$2"
  local path="$3"

  docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/grep "$IMAGE" -q "$pattern" "/etc/openvpn/$path"
}

data_absent() {
  local data_dir="$1"
  local pattern="$2"
  local path="$3"

  if docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /bin/grep "$IMAGE" -q "$pattern" "/etc/openvpn/$path"; then
    echo "unexpected pattern in $path: $pattern" >&2
    exit 1
  fi
}

assert_nat_policy() {
  local server_name="$1"
  local nat="$2"

  docker exec "$server_name" /bin/sh -ec '
    interface=""
    next=false
    for word in $(ip -4 route show default); do
      if [ "$next" = true ]; then
        interface="$word"
        break
      fi
      [ "$word" = dev ] && next=true
    done
    test -n "$interface"
    case "$2" in
      true)
        test "$(cat /proc/sys/net/ipv4/ip_forward)" = 1
        iptables -w -t nat -C POSTROUTING -s "$1" -o "$interface" -j MASQUERADE
        ;;
      false)
        if iptables -w -t nat -C POSTROUTING -s "$1" -o "$interface" -j MASQUERADE >/dev/null 2>&1; then
          echo "unexpected NAT rule" >&2
          exit 1
        fi
        ;;
    esac
  ' sh "$NETWORK" "$nat"
}

start_server() {
  local data_dir="$1"
  local endpoint="$2"
  local server_name="$3"
  local network_name="$4"

  docker rm -f "$server_name" >/dev/null 2>&1 || true
  docker run -d \
    --name "$server_name" \
    --network "$network_name" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun:/dev/net/tun \
    -e "OVPN_ENDPOINT=$endpoint" \
    -e "OVPN_NETWORK=$NETWORK" \
    -e "OVPN_PORT=$PORT" \
    -e "OVPN_PROTO=$proto" \
    -e "OVPN_TRANSPORT_FAMILY=$transport_family" \
    -e "OVPN_NAT=$POLICY_NAT" \
    -e "OVPN_NAT_INTERFACE=$POLICY_NAT_INTERFACE" \
    -e "OVPN_REDIRECT_GATEWAY=$POLICY_REDIRECT_GATEWAY" \
    -e "OVPN_CLIENT_TO_CLIENT=$POLICY_CLIENT_TO_CLIENT" \
    -e "OVPN_DNS=$POLICY_DNS" \
    -e "OVPN_ROUTES=$POLICY_ROUTES" \
    -v "$data_dir:/etc/openvpn" \
    "$IMAGE" \
    ovpn start >/dev/null
}

wait_for_log() {
  local container_name="$1"
  local pattern="$2"
  local log_path="$3"
  local deadline=$((SECONDS + WAIT_SECONDS))

  while [ "$SECONDS" -lt "$deadline" ]; do
    docker logs "$container_name" >"$log_path" 2>&1 || true
    if grep -q "$pattern" "$log_path"; then
      return 0
    fi
    if ! docker ps --format '{{.Names}}' | grep -qx "$container_name"; then
      cat "$log_path" >&2
      echo "container exited before expected log appeared: $container_name" >&2
      exit 1
    fi
    sleep 1
  done

  cat "$log_path" >&2
  echo "timed out waiting for '$pattern' in $container_name logs" >&2
  exit 1
}

run_client_until_timeout() {
  local network_name="$1"
  local profile_path="$2"
  local log_path="$3"
  local status

  set +e
  docker run --rm \
    --network "$network_name" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun:/dev/net/tun \
    -v "$profile_path:/client.ovpn:ro" \
    --entrypoint /usr/bin/timeout \
    "$IMAGE" \
    "$CONNECT_TIMEOUT" openvpn --config /client.ovpn --auth-nocache >"$log_path" 2>&1
  status=$?
  set -e

  printf '%s\n' "$status"
}

assert_client_connects() {
  local network_name="$1"
  local profile_path="$2"
  local log_path="$3"
  local status

  status="$(run_client_until_timeout "$network_name" "$profile_path" "$log_path")"
  if [ "$status" -ne 124 ]; then
    cat "$log_path" >&2
    echo "expected connected client to remain running until timeout; status=$status" >&2
    exit 1
  fi
  if ! grep -q 'Initialization Sequence Completed' "$log_path"; then
    cat "$log_path" >&2
    echo "client did not complete OpenVPN initialization" >&2
    exit 1
  fi
}

assert_client_rejected() {
  local network_name="$1"
  local profile_path="$2"
  local client_log="$3"
  local server_name="$4"
  local server_log="$5"

  run_client_until_timeout "$network_name" "$profile_path" "$client_log" >/dev/null
  docker logs "$server_name" >"$server_log" 2>&1 || true

  if grep -q 'Initialization Sequence Completed' "$client_log"; then
    cat "$client_log" >&2
    echo "revoked client unexpectedly connected" >&2
    exit 1
  fi

  if ! grep -Eiq 'certificate revoked|VERIFY ERROR|TLS Error' "$client_log" "$server_log"; then
    cat "$client_log" >&2
    cat "$server_log" >&2
    echo "revoked client did not produce an expected rejection log" >&2
    exit 1
  fi
}

for proto in udp tcp; do
  transport_family=auto
  data_dir="$WORK_DIR/$proto-data"
  profile_path="$WORK_DIR/client-$proto.ovpn"
  network_name="$RUN_ID-$proto-net"
  server_name="$RUN_ID-$proto-server"
  endpoint="$server_name"
  if [ "$proto" = udp ]; then
    POLICY_NAT=true
    POLICY_NAT_INTERFACE=auto
    POLICY_REDIRECT_GATEWAY=true
    POLICY_CLIENT_TO_CLIENT=true
    POLICY_DNS='1.1.1.1,8.8.8.8'
    POLICY_ROUTES='192.168.50.0/24'
  else
    POLICY_NAT=false
    POLICY_NAT_INTERFACE=auto
    POLICY_REDIRECT_GATEWAY=false
    POLICY_CLIENT_TO_CLIENT=false
    POLICY_DNS=''
    POLICY_ROUTES=''
  fi
  mkdir -p "$data_dir"

  docker network create "$network_name" >/dev/null
  networks+=("$network_name")
  containers+=("$server_name")

  start_server "$data_dir" "$endpoint" "$server_name" "$network_name"
  wait_for_log "$server_name" 'Initialization Sequence Completed' "$WORK_DIR/server-$proto-active.log"
  test "$(run_control "$data_dir" "$endpoint" ovpn state show)" = HEALTHY
  docker exec "$server_name" ovpn runtime health

  data_grep "$data_dir" "^OVPN_NETWORK=$NETWORK$" config/project.env
  data_grep "$data_dir" "^OVPN_PROTO=$proto$" config/project.env
  data_grep "$data_dir" '^OVPN_TRANSPORT_FAMILY=auto$' config/project.env
  data_grep "$data_dir" '^server 10.88.0.0 255.255.255.0 nopool$' server/server.conf
  data_grep "$data_dir" "^proto $proto$" server/server.conf
  data_grep "$data_dir" "^OVPN_NAT=$POLICY_NAT$" config/project.env
  data_grep "$data_dir" "^OVPN_NAT_INTERFACE=$POLICY_NAT_INTERFACE$" config/project.env
  data_grep "$data_dir" "^OVPN_REDIRECT_GATEWAY=$POLICY_REDIRECT_GATEWAY$" config/project.env
  data_grep "$data_dir" "^OVPN_CLIENT_TO_CLIENT=$POLICY_CLIENT_TO_CLIENT$" config/project.env
  data_grep "$data_dir" "^OVPN_DNS=$POLICY_DNS$" config/project.env
  data_grep "$data_dir" "^OVPN_ROUTES=$POLICY_ROUTES$" config/project.env
  if [ "$POLICY_REDIRECT_GATEWAY" = true ]; then
    data_grep "$data_dir" '^push "redirect-gateway def1"$' server/server.conf
    data_grep "$data_dir" '^push "route 192.168.50.0 255.255.255.0"$' server/server.conf
    data_grep "$data_dir" '^push "dhcp-option DNS 1.1.1.1"$' server/server.conf
    data_grep "$data_dir" '^push "dhcp-option DNS 8.8.8.8"$' server/server.conf
    data_grep "$data_dir" '^client-to-client$' server/server.conf
  else
    data_absent "$data_dir" '^push "redirect-gateway def1"$' server/server.conf
    data_absent "$data_dir" '^push "route 192.168.50.0 255.255.255.0"$' server/server.conf
    data_absent "$data_dir" '^push "dhcp-option DNS 1.1.1.1"$' server/server.conf
    data_absent "$data_dir" '^client-to-client$' server/server.conf
  fi
  assert_nat_policy "$server_name" "$POLICY_NAT"

  run_control "$data_dir" "$endpoint" ovpn client create "client-$proto"
  run_control "$data_dir" "$endpoint" ovpn client export "client-$proto" >"$profile_path"
  grep -q "^remote $endpoint $PORT$" "$profile_path"
  grep -q "^proto $proto$" "$profile_path"
  grep -E "^client-$proto[[:space:]]+active$" <(run_control "$data_dir" "$endpoint" ovpn client list)

  assert_client_connects "$network_name" "$profile_path" "$WORK_DIR/client-$proto-active.log"

  docker rm -f "$server_name" >/dev/null
  run_control "$data_dir" "$endpoint" ovpn client revoke "client-$proto"
  grep -E "^client-$proto[[:space:]]+revoked$" <(run_control "$data_dir" "$endpoint" ovpn client list)

  start_server "$data_dir" "$endpoint" "$server_name" "$network_name"
  wait_for_log "$server_name" 'Initialization Sequence Completed' "$WORK_DIR/server-$proto-revoked-start.log"
  assert_client_rejected "$network_name" "$profile_path" "$WORK_DIR/client-$proto-revoked.log" "$server_name" "$WORK_DIR/server-$proto-revoked.log"

done

family_index=0
for proto in udp tcp; do
  family_index=$((family_index + 1))
  transport_family=ipv6
  data_dir="$WORK_DIR/$proto-ipv6-data"
  profile_path="$WORK_DIR/client-$proto-ipv6.ovpn"
  network_name="$RUN_ID-$proto-ipv6-net"
  server_name="$RUN_ID-$proto-ipv6-server"
  endpoint="$server_name"
  POLICY_NAT=false
  POLICY_NAT_INTERFACE=auto
  POLICY_REDIRECT_GATEWAY=false
  POLICY_CLIENT_TO_CLIENT=true
  POLICY_DNS=''
  POLICY_ROUTES=''
  mkdir -p "$data_dir"

  docker network create --ipv6 --subnet "fd42:88:$family_index::/64" "$network_name" >/dev/null
  networks+=("$network_name")
  containers+=("$server_name")

  start_server "$data_dir" "$endpoint" "$server_name" "$network_name"
  wait_for_log "$server_name" 'Initialization Sequence Completed' "$WORK_DIR/server-$proto-ipv6.log"
  docker exec "$server_name" ovpn runtime health

  if [ "$proto" = udp ]; then
    server_proto=udp6
    client_proto=udp6
  else
    server_proto=tcp6-server
    client_proto=tcp6-client
  fi
  data_grep "$data_dir" '^OVPN_TRANSPORT_FAMILY=ipv6$' config/project.env
  data_grep "$data_dir" "^proto $server_proto$" server/server.conf
  data_grep "$data_dir" '^server 10.88.0.0 255.255.255.0 nopool$' server/server.conf
  data_absent "$data_dir" 'ifconfig-ipv6\|route-ipv6' server/server.conf

  run_control "$data_dir" "$endpoint" ovpn client create "client-$proto-ipv6"
  run_control "$data_dir" "$endpoint" ovpn client export "client-$proto-ipv6" >"$profile_path"
  grep -Fqx "proto $client_proto" "$profile_path"
  grep -Fqx "remote $endpoint $PORT" "$profile_path"
  if grep -Eq 'ifconfig-ipv6|route-ipv6' "$profile_path"; then
    echo 'IPv6 transport profile unexpectedly configured an IPv6 tunnel' >&2
    exit 1
  fi

  assert_client_connects "$network_name" "$profile_path" "$WORK_DIR/client-$proto-ipv6.log"
  grep -Eq 'UDPv6|TCPv6' "$WORK_DIR/client-$proto-ipv6.log"
done

printf 'e2e container smoke passed (IPv4 and IPv6 transport; udp,tcp network=%s)\n' "$NETWORK"
