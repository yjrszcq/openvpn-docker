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
UPGRADE_REQUIRED="${OVPN_E2E_UPGRADE_REQUIRED:-0}"
UPGRADE_SIGNING_KEY="${OVPN_E2E_MANAGEMENT_SIGNING_KEY:-}"
RUN_ID="ovpn-e2e-$$-$(date +%s)"
CONTROL_RUNTIME_DIR="/tmp/openvpn-container-$RUN_ID"
POLICY_NAT=true
POLICY_NAT_INTERFACE=auto
POLICY_REDIRECT_GATEWAY=false
POLICY_CLIENT_TO_CLIENT=false
POLICY_DNS=''
POLICY_ROUTES=''
transport_family=auto
UPDATE_FIXTURES=''

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

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-e2e.XXXXXX")"

if [ -z "$UPGRADE_SIGNING_KEY" ] && [ "$SKIP_BUILD" != 1 ]; then
  UPGRADE_SIGNING_KEY="$WORK_DIR/management-signing-key.pem"
  openssl genpkey -algorithm ED25519 -out "$UPGRADE_SIGNING_KEY" >/dev/null 2>&1
fi

if [ "$SKIP_BUILD" != 1 ]; then
  build_arguments=()
  if [ -n "$UPGRADE_SIGNING_KEY" ]; then
    public_key="$WORK_DIR/management-signing-public.pem"
    openssl pkey -in "$UPGRADE_SIGNING_KEY" -pubout -out "$public_key" >/dev/null 2>&1
    public_key_b64="$(base64 -w 0 "$public_key")"
    build_arguments+=(--build-arg "MANAGEMENT_SIGNING_PUBLIC_KEY_B64=$public_key_b64")
  fi
  "$ROOT_DIR/scripts/docker-build.sh" "${build_arguments[@]}" -t "$IMAGE" "$ROOT_DIR"
fi

if [ -n "$UPGRADE_SIGNING_KEY" ]; then
  UPDATE_FIXTURES="$WORK_DIR/update-fixtures"
  mkdir -p "$UPDATE_FIXTURES/3.0.1"
  "$ROOT_DIR/tests/helpers/create-management-release-fixture.sh" \
    --output-dir "$UPDATE_FIXTURES/3.0.1" \
    --signing-key "$UPGRADE_SIGNING_KEY" \
    --version 3.0.1
  cp "$ROOT_DIR/tests/helpers/fake-release-curl.sh" "$UPDATE_FIXTURES/fake-release-curl"
  chmod +x "$UPDATE_FIXTURES/fake-release-curl"
  jq -n ' [{
    tag_name:"v3.0.1", draft:false, prerelease:false, assets:[
      {name:"management-release.env",browser_download_url:"file:///update-fixtures/3.0.1/management-release.env"},
      {name:"management-release.env.sig",browser_download_url:"file:///update-fixtures/3.0.1/management-release.env.sig"},
      {name:"management-bundle.tar.gz",browser_download_url:"file:///update-fixtures/3.0.1/management-bundle.tar.gz"}
    ]
  }] ' >"$UPDATE_FIXTURES/releases.json"
elif [ "$UPGRADE_REQUIRED" = 1 ]; then
  skip_or_fail 'online-update E2E requires OVPN_E2E_MANAGEMENT_SIGNING_KEY for the prebuilt image'
fi

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
  local -a update_arguments=()
  if [ -n "$UPDATE_FIXTURES" ]; then
    update_arguments+=(
      -e OVPN_CURL_BIN=/update-fixtures/fake-release-curl
      -e OVPN_GITHUB_API_URL=mock://repository
      -e UPGRADE_FIXTURES=/update-fixtures
      -v "$UPDATE_FIXTURES:/update-fixtures:ro"
    )
  fi

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
    "${update_arguments[@]}" \
    -v "$data_dir:/etc/openvpn" \
    "$IMAGE" \
    ovpn start >/dev/null
}

start_persistent_client() {
  local network_name="$1"
  local profile_path="$2"
  local client_name="$3"

  docker run -d \
    --name "$client_name" \
    --network "$network_name" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun:/dev/net/tun \
    -v "$profile_path:/client.ovpn:ro" \
    --entrypoint /usr/local/sbin/openvpn \
    "$IMAGE" \
    --config /client.ovpn --auth-nocache >/dev/null
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

wait_for_runtime_log() {
  local container_name="$1"
  local pattern="$2"
  local log_path="$3"
  local deadline=$((SECONDS + WAIT_SECONDS))

  while [ "$SECONDS" -lt "$deadline" ]; do
    docker exec "$container_name" ovpn runtime logs --lines 300 >"$log_path"
    grep -Fq "$pattern" "$log_path" && return 0
    sleep 1
  done
  cat "$log_path" >&2
  echo "persistent runtime log did not contain: $pattern" >&2
  exit 1
}

wait_for_runtime_event() {
  local container_name="$1"
  local expression="$2"
  local output_path="$3"
  local deadline=$((SECONDS + WAIT_SECONDS))

  while [ "$SECONDS" -lt "$deadline" ]; do
    docker exec "$container_name" ovpn runtime events --lines 300 --json >"$output_path"
    jq -e -s "$expression" "$output_path" >/dev/null && return 0
    sleep 1
  done
  cat "$output_path" >&2
  echo "persistent runtime event did not match: $expression" >&2
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
  docker exec "$server_name" sh -ec '
    test -S /run/openvpn-container/management.sock
    test -S /run/openvpn-container/openvpn-management.sock
    test -s /run/openvpn-container/management-broker.pid
    kill -0 "$(cat /run/openvpn-container/management-broker.pid)"
  '

  data_grep "$data_dir" "^OVPN_NETWORK=$NETWORK$" config/project.env
  data_grep "$data_dir" "^OVPN_PROTO=$proto$" config/project.env
  data_grep "$data_dir" '^OVPN_TRANSPORT_FAMILY=auto$' config/project.env
  data_grep "$data_dir" '^server 10.88.0.0 255.255.255.0 nopool$' server/server.conf
  if [ "$proto" = udp ]; then
    server_proto=udp6
  else
    server_proto=tcp6-server
  fi
  data_grep "$data_dir" "^proto $server_proto$" server/server.conf
  data_absent "$data_dir" "^bind ipv6only$" server/server.conf
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
  client_id="$(docker run --rm -v "$data_dir:/etc/openvpn:ro" --entrypoint /usr/bin/awk "$IMAGE" \
    -F, -v name="client-$proto" '$2 == name && $3 == "active" { print $1 }' /etc/openvpn/meta/client-state.csv)"
  client_display="client-$proto"
  run_control "$data_dir" "$endpoint" ovpn client export "client-$proto" >"$profile_path"
  grep -q "^remote $endpoint $PORT$" "$profile_path"
  grep -q "^proto $proto$" "$profile_path"
  grep -E "^client-${proto}[[:space:]]+[0-9a-f-]{36}[[:space:]]+active$" <(run_control "$data_dir" "$endpoint" ovpn client list)

  assert_client_connects "$network_name" "$profile_path" "$WORK_DIR/client-$proto-active.log"
  wait_for_runtime_log "$server_name" "client-$proto [$client_id]" \
    "$WORK_DIR/runtime-$proto.log"
  wait_for_runtime_event "$server_name" \
    "any(.[]; .event == \"client_connection\" and .operation == \"connect\" and .client_id == \"$client_id\" and .client_name == \"client-$proto\")" \
    "$WORK_DIR/events-$proto.jsonl"
  docker exec "$server_name" ovpn runtime logs --lines 300 --raw \
    >"$WORK_DIR/runtime-$proto-raw.log"
  grep -Fq "$client_id" "$WORK_DIR/runtime-$proto-raw.log"
  if grep -Fq "client-$proto [$client_id]" "$WORK_DIR/runtime-$proto-raw.log"; then
    echo 'raw runtime log unexpectedly contained translated identity' >&2
    exit 1
  fi

  if [ "$proto" = udp ]; then
    concurrent_pids=()
    for index in 1 2 3 4 5 6 7 8; do
      docker exec "$server_name" ovpn client list --detail \
        >"$WORK_DIR/client-list-concurrent-$index" &
      concurrent_pids+=("$!")
    done
    for index in "${!concurrent_pids[@]}"; do
      wait "${concurrent_pids[$index]}"
      grep -E "^client-udp[[:space:]]+${client_id}[[:space:]]+active" \
        "$WORK_DIR/client-list-concurrent-$((index + 1))"
    done

    rename_client="$RUN_ID-rename-client"
    containers+=("$rename_client")
    start_persistent_client "$network_name" "$profile_path" "$rename_client"
    wait_for_log "$rename_client" 'Initialization Sequence Completed' "$WORK_DIR/client-rename-active.log"
    openvpn_pid="$(docker exec "$server_name" sh -ec 'pgrep -xo openvpn')"
    run_control "$data_dir" "$endpoint" ovpn client rename "$client_id" renamed-udp
    client_display=renamed-udp
    [ "$(docker exec "$server_name" sh -ec 'pgrep -xo openvpn')" = "$openvpn_pid" ]
    docker ps --format '{{.Names}}' | grep -Fqx "$rename_client"
    docker exec "$rename_client" ip -4 address show dev tun0 | grep -Fq 'inet '
    grep -E "^renamed-udp[[:space:]]+${client_id}[[:space:]]+active.*online$" \
      <(docker exec "$server_name" ovpn client list --detail)
    wait_for_runtime_log "$server_name" "renamed-udp [$client_id]" \
      "$WORK_DIR/runtime-renamed-udp.log"
    wait_for_runtime_event "$server_name" \
      "any(.[]; .event == \"client_lifecycle\" and .operation == \"rename\" and .client_id == \"$client_id\" and .client_name == \"renamed-udp\" and .old_name == \"client-udp\")" \
      "$WORK_DIR/events-renamed-udp.jsonl"
    docker rm -f "$rename_client" >/dev/null
  fi

  if [ "$proto" = udp ] && [ -n "$UPDATE_FIXTURES" ]; then
    update_client="$RUN_ID-upgrade-client"
    containers+=("$update_client")
    start_persistent_client "$network_name" "$profile_path" "$update_client"
    wait_for_log "$update_client" 'Initialization Sequence Completed' "$WORK_DIR/client-upgrade-active.log"
    openvpn_pid="$(docker exec "$server_name" sh -ec 'pgrep -xo openvpn')"
    broker_pid="$(docker exec "$server_name" cat /run/openvpn-container/management-broker.pid)"
    docker exec "$server_name" timeout 30 ovpn runtime logs --lines 0 --follow \
      >"$WORK_DIR/log-follow-upgrade.out" &
    log_follow_pid=$!
    docker exec "$server_name" timeout 30 ovpn runtime events --lines 0 --follow --json \
      >"$WORK_DIR/event-follow-upgrade.out" &
    event_follow_pid=$!
    docker exec "$server_name" ovpn client list --detail >"$WORK_DIR/client-list-before-upgrade"
    grep -E "^${client_display}[[:space:]]+[0-9a-f-]{36}[[:space:]]+active.*online$" "$WORK_DIR/client-list-before-upgrade"

    docker exec "$server_name" ovpn upgrade --yes >"$WORK_DIR/upgrade.out"
    [ "$(docker exec "$server_name" ovpn -v)" = 3.0.1 ]
    [ "$(docker exec "$server_name" sh -ec 'pgrep -xo openvpn')" = "$openvpn_pid" ]
    [ "$(docker exec "$server_name" cat /run/openvpn-container/management-broker.pid)" = "$broker_pid" ]
    docker exec "$server_name" sh -ec \
      'printf "broker-health\nquit\n" | socat - UNIX-CONNECT:/run/openvpn-container/management.sock' |
      grep -Fq 'SUCCESS: broker connected to OpenVPN'
    docker ps --format '{{.Names}}' | grep -Fqx "$update_client"
    docker exec "$update_client" ip -4 address show dev tun0 | grep -Fq 'inet '
    docker exec "$server_name" ovpn client list --detail >"$WORK_DIR/client-list-after-upgrade"
    grep -E "^${client_display}[[:space:]]+[0-9a-f-]{36}[[:space:]]+active.*online$" "$WORK_DIR/client-list-after-upgrade"
    wait_for_runtime_event "$server_name" \
      'any(.[]; .event == "management_upgrade" and .operation == "apply" and .from_version == "3.0.0" and .to_version == "3.0.1")' \
      "$WORK_DIR/events-upgrade.jsonl"
    grep -Fq '"event":"management_upgrade"' "$WORK_DIR/event-follow-upgrade.out"
    kill -0 "$log_follow_pid"
    kill -0 "$event_follow_pid"

    docker exec "$server_name" ovpn upgrade --rollback --yes >"$WORK_DIR/rollback.out"
    [ "$(docker exec "$server_name" ovpn -v)" = 3.0.0 ]
    [ "$(docker exec "$server_name" sh -ec 'pgrep -xo openvpn')" = "$openvpn_pid" ]
    [ "$(docker exec "$server_name" cat /run/openvpn-container/management-broker.pid)" = "$broker_pid" ]
    docker ps --format '{{.Names}}' | grep -Fqx "$update_client"
    wait_for_runtime_event "$server_name" \
      'any(.[]; .event == "management_upgrade" and .operation == "rollback" and .from_version == "3.0.1" and .to_version == "embedded")' \
      "$WORK_DIR/events-rollback.jsonl"
    kill "$log_follow_pid" "$event_follow_pid" >/dev/null 2>&1 || true
    wait "$log_follow_pid" "$event_follow_pid" 2>/dev/null || true
    docker rm -f "$update_client" >/dev/null
  fi

  docker rm -f "$server_name" >/dev/null
  run_control "$data_dir" "$endpoint" ovpn client revoke "$client_id"
  grep -E "^${client_display}[[:space:]]+${client_id}[[:space:]]+revoked$" <(run_control "$data_dir" "$endpoint" ovpn client list)

  start_server "$data_dir" "$endpoint" "$server_name" "$network_name"
  wait_for_log "$server_name" 'Initialization Sequence Completed' "$WORK_DIR/server-$proto-revoked-start.log"
  assert_client_rejected "$network_name" "$profile_path" "$WORK_DIR/client-$proto-revoked.log" "$server_name" "$WORK_DIR/server-$proto-revoked.log"

done

family_index=0
for proto in udp tcp; do
  family_index=$((family_index + 1))
  transport_family=auto
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

  docker network create --ipv4=false --ipv6 --subnet "fd42:88:$family_index::/64" "$network_name" >/dev/null
  networks+=("$network_name")
  containers+=("$server_name")

  start_server "$data_dir" "$endpoint" "$server_name" "$network_name"
  wait_for_log "$server_name" 'Initialization Sequence Completed' "$WORK_DIR/server-$proto-ipv6.log"
  docker exec "$server_name" ovpn runtime health

  if [ "$proto" = udp ]; then
    server_proto=udp6
    client_proto=udp
  else
    server_proto=tcp6-server
    client_proto=tcp
  fi
  data_grep "$data_dir" '^OVPN_TRANSPORT_FAMILY=auto$' config/project.env
  data_grep "$data_dir" "^proto $server_proto$" server/server.conf
  data_absent "$data_dir" "^bind ipv6only$" server/server.conf
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

printf 'e2e container smoke passed (auto hostname over IPv4 and IPv6; udp,tcp tunnel=%s)\n' "$NETWORK"
