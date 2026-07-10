#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OVPN_CONFIG_LOAD_IMAGE:-szcq/openvpn-server:config-load}"
REQUIRED="${OVPN_CONFIG_LOAD_REQUIRED:-0}"
SKIP_BUILD="${OVPN_CONFIG_LOAD_SKIP_BUILD:-0}"
NETWORK="10.88.0.0/24"

skip_or_fail() {
  local reason="$1"
  if [ "$REQUIRED" = 1 ]; then
    printf 'config load smoke failed: %s\n' "$reason" >&2
    exit 1
  fi
  printf 'config load smoke skipped: %s\n' "$reason"
  exit 0
}

if ! command -v docker >/dev/null 2>&1; then
  skip_or_fail 'missing command: docker'
fi
if ! docker info >/dev/null 2>&1; then
  skip_or_fail 'Docker daemon is not accessible'
fi

if [ "$SKIP_BUILD" != 1 ]; then
  "$ROOT_DIR/scripts/docker-build.sh" -t "$IMAGE" "$ROOT_DIR"
fi

docker run --rm \
  -e OVPN_ENDPOINT=compat.example.test \
  -e "OVPN_NETWORK=$NETWORK" \
  -e OVPN_PROTO=udp \
  --entrypoint /bin/bash \
  "$IMAGE" \
  -ec 'ovpn capabilities >/tmp/capabilities.json
       grep -Fq "\"supported_range\": true" /tmp/capabilities.json
       grep -Fq "\"adapter\": \"openvpn-2.7\"" /tmp/capabilities.json
       grep -Fq "\"tls_crypt\": true" /tmp/capabilities.json
       grep -Fq "\"data_ciphers\": true" /tmp/capabilities.json
       grep -Fq "\"crl_verify\": true" /tmp/capabilities.json
       grep -Fq "\"topology_subnet\": true" /tmp/capabilities.json
       ovpn init >/tmp/init.log 2>&1
       ovpn add-client config-load >/tmp/add-client.log 2>&1
       ovpn export-client config-load >/tmp/config-load.ovpn
       openvpn --config /etc/openvpn/server/server.conf --cipher AES-256-GCM --test-crypto >/tmp/server-config-load.log 2>&1
       openvpn --config /tmp/config-load.ovpn --cipher AES-256-GCM --test-crypto >/tmp/client-config-load.log 2>&1
       grep -Fq "crypto self-test mode SUCCEEDED" /tmp/server-config-load.log
       grep -Fq "crypto self-test mode SUCCEEDED" /tmp/client-config-load.log'

printf 'config load smoke passed (network=%s)\n' "$NETWORK"
