#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
IMAGE="${OVPN_E2E_IMAGE:-szcq/openvpn-server:e2e-smoke}"
REQUIRED="${OVPN_E2E_REQUIRED:-0}"
SKIP_BUILD="${OVPN_E2E_SKIP_BUILD:-0}"
WORK_DIR=''
active_containers=()
active_networks=()

skip_or_fail() {
  if [ "$REQUIRED" = 1 ]; then
    printf 'E2E smoke failed: %s\n' "$1" >&2
    exit 1
  fi
  printf 'E2E smoke skipped: %s\n' "$1"
  exit 0
}

cleanup() {
  for container in "${active_containers[@]}"; do
    docker rm -f "$container" >/dev/null 2>&1 || true
  done
  for network in "${active_networks[@]}"; do
    docker network rm "$network" >/dev/null 2>&1 || true
  done
  if [ -n "$WORK_DIR" ]; then
    docker run --rm -v "$WORK_DIR:/work" --entrypoint sh "$IMAGE" -ec 'rm -rf /work/*' >/dev/null 2>&1 || true
    rmdir "$WORK_DIR" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || skip_or_fail 'missing command: docker'
command -v curl >/dev/null 2>&1 || skip_or_fail 'missing command: curl'
command -v jq >/dev/null 2>&1 || skip_or_fail 'missing command: jq'
docker info >/dev/null 2>&1 || skip_or_fail 'Docker daemon is not accessible'
[ -c /dev/net/tun ] || skip_or_fail 'host /dev/net/tun is not available'

if [ "$SKIP_BUILD" != 1 ]; then
  OVPN_BUILD_NETWORK=host "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  skip_or_fail "image not found: $IMAGE"
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ovpn-e2e.XXXXXX")"

run_protocol() {
  local protocol="$1"
  local index="$2"
  local family="${3:-auto}"
  local protocol_dir="$WORK_DIR/$protocol-$family"
  local data_dir="$protocol_dir/data"
  local config_dir="$protocol_dir/config"
  local server="ovpn-e2e-$protocol-$family-$$"
  local client_container="$server-client"
  local network="ovpn-e2e-$protocol-$family-net-$$"
  local client="e2e-$protocol"
  local target="$server-target"
  local peer=''
  local ipv4_intent=dynamic
  if [ "$protocol" = tcp ]; then ipv4_intent=auto; fi
  local target_address="10.$((160 + index)).0.250"

  mkdir -p "$protocol_dir"
  mkdir -m 0750 "$data_dir" "$config_dir"
  cat >"$config_dir/config.yaml" <<YAML
version: 1
server:
  endpoint: $server
  transport:
    protocol: $protocol
    family: $family
    port: 1194
  clientToClient: true
ipv4:
  network: 10.$((100 + index)).0.0/24
  dynamicPoolSize: 64
  nat:
    enabled: true
    interface: auto
  redirectGateway: true
  dns: [1.1.1.1]
  routes: [$target_address/32]
logging:
  maxBytes: 10485760
  backups: 5
YAML

  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/ovpn-conf" \
    --entrypoint ovpn \
    "$IMAGE" server init >"$protocol_dir/init.out"
  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/ovpn-conf" \
    --entrypoint ovpn \
    "$IMAGE" client create "$client" --ipv4 "$ipv4_intent" >"$protocol_dir/create.out"
  docker run --rm \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/ovpn-conf" \
    --entrypoint ovpn \
    "$IMAGE" client export --name "$client" --output - >"$protocol_dir/client.ovpn"

  if [ "$family" = ipv6 ]; then
    docker network create --ipv6 --subnet "fd42:$index::/64" \
      --subnet "10.$((160 + index)).0.0/24" "$network" >/dev/null
  else
    docker network create --subnet "10.$((160 + index)).0.0/24" "$network" >/dev/null
  fi
  active_networks+=("$network")
  docker run -d \
    --name "$server" \
    -e OVPN_API_LISTEN=0.0.0.0:11940 -p 127.0.0.1::11940 \
    --network "$network" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun \
    -v "$data_dir:/etc/openvpn" \
    -v "$config_dir:/etc/ovpn-conf" \
    "$IMAGE" >"$protocol_dir/server.id"
  active_containers+=("$server")
  docker run -d --name "$target" --network "$network" --ip "$target_address" \
    -v "$data_dir:/etc/openvpn:ro" --entrypoint openssl "$IMAGE" \
    s_server -quiet -www -accept 18443 \
    -cert /etc/openvpn/pki/issued/openvpn-server.crt \
    -key /etc/openvpn/pki/private/openvpn-server.key >"$protocol_dir/target.id"
  active_containers+=("$target")

  for _ in $(seq 1 60); do
    if docker exec "$server" ovpn runtime health >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
  if ! docker exec "$server" ovpn runtime health; then
    docker logs "$server" >&2 || true
    return 1
  fi

  local api_port api_url api_key
  api_port="$(docker port "$server" 11940/tcp | sed -n '1s/.*://p')"
  api_url="http://127.0.0.1:$api_port"
  api_key="$(docker exec "$server" ovpn api key create e2e --json | jq -er '.secret')"

  if [ "$protocol" = udp ]; then
    sed -i 's/maxBytes: 10485760/maxBytes: 2097152/' "$config_dir/config.yaml"
    docker exec "$server" ovpn config plan --json >"$protocol_dir/apply-plan.json"
    docker exec "$server" ovpn config apply --yes --json >"$protocol_dir/apply-while-running.json"
    grep -Fq '"applied":true' "$protocol_dir/apply-while-running.json"
    grep -Fq '"restart_required":false' "$protocol_dir/apply-while-running.json"
    grep -Fq '"runtime_restarted":true' "$protocol_dir/apply-while-running.json"
    docker exec "$server" ovpn runtime health
  fi

  # Apply a real desired-state change through the supervised REST API.
  local desired_digest current_revision
  curl -fsS -H "Authorization: Bearer $api_key" "$api_url/api/v1/config/desired" >"$protocol_dir/desired.json"
  desired_digest="$(jq -er '.digest' "$protocol_dir/desired.json")"
  jq '.config | .logging.backups = 2' "$protocol_dir/desired.json" >"$protocol_dir/desired-update.json"
  curl -fsS -X PUT -H "Authorization: Bearer $api_key" \
    -H 'Content-Type: application/json' -H "If-Match: \"$desired_digest\"" \
    --data-binary "@$protocol_dir/desired-update.json" "$api_url/api/v1/config/desired" >"$protocol_dir/desired-updated.json"
  desired_digest="$(jq -er '.digest' "$protocol_dir/desired-updated.json")"
  current_revision="$(curl -fsS -H "Authorization: Bearer $api_key" "$api_url/api/v1/config/applied" | jq -er '.revision')"
  jq -n --arg digest "$desired_digest" --argjson revision "$current_revision" \
    '{desired_digest:$digest,current_revision:$revision,force:false}' >"$protocol_dir/apply-request.json"
  curl -fsS -X POST -H "Authorization: Bearer $api_key" -H 'Content-Type: application/json' \
    --data-binary "@$protocol_dir/apply-request.json" "$api_url/api/v1/config/apply" >"$protocol_dir/api-apply.json"
  jq -e '.applied and .activation.runtime_restarted' "$protocol_dir/api-apply.json" >/dev/null
  curl -fsS -H "Authorization: Bearer $api_key" "$api_url/api/v1/config/applied" |
    jq -e '.config.logging.backups == 2' >/dev/null
  docker exec "$server" ovpn runtime health

  set +e
  docker run --rm \
    --name "$client_container" \
    --network "$network" \
    --cap-add NET_ADMIN \
    --device /dev/net/tun \
    -v "$protocol_dir/client.ovpn:/client.ovpn:ro" \
    --entrypoint /usr/bin/timeout \
    "$IMAGE" 20s openvpn --config /client.ovpn --verb 4 >"$protocol_dir/client.log" 2>&1 &
  local client_process=$!
  active_containers+=("$client_container")
  set -e

  for _ in $(seq 1 60); do
    if grep -Fq 'Initialization Sequence Completed' "$protocol_dir/client.log"; then
      break
    fi
    sleep 0.5
  done
  if ! grep -Fq 'Initialization Sequence Completed' "$protocol_dir/client.log"; then
    cat "$protocol_dir/client.log" >&2
    docker logs "$server" >&2 || true
    return 1
  fi

  local runtime_status
  runtime_status="$(docker exec "$server" ovpn runtime status --json)"
  if ! grep -Fq '"client_count":1' <<<"$runtime_status" || ! grep -Fq "\"client_name\":\"$client\"" <<<"$runtime_status"; then
    printf '%s\n' "$runtime_status" >&2
    return 1
  fi

  if [ "$ipv4_intent" = auto ]; then
    jq -e --arg address "10.$((100 + index)).0.2" \
      '.clients[0].virtual_address == $address' <<<"$runtime_status" >/dev/null
  fi

  curl -fsS -H "Authorization: Bearer $api_key" "$api_url/api/v1/runtime" >"$protocol_dir/api-runtime.json"
  jq -e '.client_count == 1' "$protocol_dir/api-runtime.json" >/dev/null

  # Verify application data traverses the TUN device, in addition to the
  # control-plane connection report. OpenSSL is already in the runtime image.
  docker exec -d "$server" openssl s_server -quiet -www -accept 18443 \
    -cert /etc/openvpn/pki/issued/openvpn-server.crt \
    -key /etc/openvpn/pki/private/openvpn-server.key
  local tunnel_server="10.$((100 + index)).0.1"
  for _ in $(seq 1 20); do
    if docker exec "$client_container" bash -ec \
      'printf "GET / HTTP/1.0\r\n\r\n" | timeout 5 openssl s_client -quiet -connect "$1:18443" 2>/dev/null' \
      probe "$tunnel_server" >"$protocol_dir/tunnel-http.out" 2>/dev/null &&
      grep -Fq 'HTTP/1.0 200 ok' "$protocol_dir/tunnel-http.out"; then
      break
    fi
    sleep 0.1
  done
  grep -Fq 'HTTP/1.0 200 ok' "$protocol_dir/tunnel-http.out"
  # A pushed host route must carry traffic through the VPN to another
  # container; successful return traffic also exercises forwarding and NAT.
  docker exec "$client_container" ip -4 route get "$target_address" >"$protocol_dir/target-route.out"
  grep -Fq 'dev tun' "$protocol_dir/target-route.out"
  docker exec "$client_container" bash -ec \
    'printf "GET / HTTP/1.0\r\n\r\n" | timeout 5 openssl s_client -quiet -connect "$1:18443" 2>/dev/null' \
    probe "$target_address" >"$protocol_dir/forwarded-http.out"
  grep -Fq 'HTTP/1.0 200 ok' "$protocol_dir/forwarded-http.out"
  docker exec "$server" iptables -t nat -L -v -n -x >"$protocol_dir/nat.out"
  awk '/MASQUERADE/ && $1 > 0 { found=1 } END { exit !found }' "$protocol_dir/nat.out"
  docker exec "$client_container" ip -4 route get 1.1.1.1 >"$protocol_dir/default-route.out"
  grep -Fq 'dev tun' "$protocol_dir/default-route.out"
  grep -Fq 'dhcp-option DNS 1.1.1.1' "$protocol_dir/client.log"
  if [ "$protocol" = udp ] && [ "$family" = auto ]; then
    # A second identity proves static CCD assignment and client-to-client data.
    peer="$server-peer"
    docker exec "$server" ovpn client create e2e-peer --ipv4 "10.$((100 + index)).0.20" \
      --output - >"$protocol_dir/peer.ovpn"
    docker run -d --name "$peer" --network "$network" --cap-add NET_ADMIN --device /dev/net/tun \
      -v "$protocol_dir/peer.ovpn:/client.ovpn:ro" -v "$data_dir:/probe-data:ro" \
      --entrypoint openvpn "$IMAGE" --config /client.ovpn >"$protocol_dir/peer.id"
    active_containers+=("$peer")
    for _ in $(seq 1 40); do
      if docker logs "$peer" 2>&1 | grep -Fq 'Initialization Sequence Completed'; then break; fi
      sleep 0.1
    done
    docker logs "$peer" >"$protocol_dir/peer.log" 2>&1
    grep -Fq 'Initialization Sequence Completed' "$protocol_dir/peer.log"
    docker exec -d "$peer" openssl s_server -quiet -www -accept 18443 \
      -cert /probe-data/pki/issued/openvpn-server.crt -key /probe-data/pki/private/openvpn-server.key
    docker exec "$client_container" bash -ec \
      'printf "GET / HTTP/1.0\r\n\r\n" | timeout 5 openssl s_client -quiet -connect "$1:18443" 2>/dev/null' \
      probe "10.$((100 + index)).0.20" >"$protocol_dir/peer-http.out"
    grep -Fq 'HTTP/1.0 200 ok' "$protocol_dir/peer-http.out"
  fi
  docker exec "$server" ovpn runtime logs --lines 10 --raw >"$protocol_dir/runtime-logs.out"
  test -s "$protocol_dir/runtime-logs.out"
  docker exec "$server" ovpn runtime events --lines 10 --json >"$protocol_dir/runtime-events.jsonl"

  if [ "$protocol" = udp ]; then
    local address_result
    address_result="$(docker exec "$server" ovpn client address set "$client" --ipv4 dynamic --json)"
    if ! grep -Fq '"status":"disconnected"' <<<"$address_result"; then
      printf '%s\n' "$address_result" >&2
      return 1
    fi
  else
    local disconnect_result
    disconnect_result="$(docker exec "$server" ovpn runtime disconnect "$client" --json)"
    if ! grep -Fq '"was_connected":true' <<<"$disconnect_result" || ! grep -Fq '"disconnected":true' <<<"$disconnect_result"; then
      printf '%s\n' "$disconnect_result" >&2
      return 1
    fi
  fi

  set +e
  wait "$client_process"
  local status=$?
  set -e
  if { [ "$status" -ne 0 ] && [ "$status" -ne 124 ]; } || ! grep -Fq 'Initialization Sequence Completed' "$protocol_dir/client.log"; then
    cat "$protocol_dir/client.log" >&2
    docker logs "$server" >&2 || true
    return 1
  fi

  docker rm -f "$server" "$target" >/dev/null
  if [ -n "$peer" ]; then docker rm -f "$peer" >/dev/null; fi
  docker network rm "$network" >/dev/null
}

run_protocol udp 1
run_protocol tcp 2
run_protocol udp 3 ipv6
run_protocol tcp 4 ipv6
printf 'Go UDP/TCP E2E smoke passed (IPv4/IPv6 transport, static/dynamic TUN, client-to-client HTTPS, NAT, routes, DNS, live API apply)\n'
