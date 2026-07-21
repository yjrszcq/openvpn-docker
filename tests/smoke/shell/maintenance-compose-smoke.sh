#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  printf 'maintenance compose smoke skipped: docker compose is unavailable\n'
  exit 0
fi

docker compose --profile maintenance -f "$ROOT_DIR/docker-compose.yaml" config >"$TMP_DIR/compose.yaml"
openvpn_service="$(awk '
  /^  openvpn:$/ { inside = 1; next }
  inside && /^  [^[:space:]]/ { exit }
  inside { print }
' "$TMP_DIR/compose.yaml")"
maintenance_service="$(awk '
  /^  openvpn-maintenance:$/ { inside = 1; next }
  inside && /^  [^[:space:]]/ { exit }
  inside { print }
' "$TMP_DIR/compose.yaml")"

printf '%s\n' "$openvpn_service" | grep -Fq 'network_mode: host'
for variable in OVPN_GITHUB_TOKEN HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY; do
  if printf '%s\n' "$openvpn_service" | grep -Fq "$variable:"; then
    echo "runtime service must not expose online-update variable $variable" >&2
    exit 1
  fi
done
printf '%s\n' "$openvpn_service" | grep -Fq '/etc/openvpn-config'
for variable in OVPN_BOOTSTRAP_FROM_ENV OVPN_BOOTSTRAP_ENDPOINT OVPN_BOOTSTRAP_IPV4_NETWORK; do
  printf '%s\n' "$openvpn_service" | grep -Fq "$variable:"
done
printf '%s\n' "$openvpn_service" | grep -Fq 'OVPN_BOOTSTRAP_FROM_ENV: "false"'
for variable in OVPN_ENDPOINT OVPN_NETWORK OVPN_TOPOLOGY OVPN_DYNAMIC_POOL_SIZE OVPN_LOG_MAX_BYTES OVPN_CRITICAL_MODE; do
  if printf '%s\n' "$openvpn_service" | grep -Fq "$variable:"; then
    echo "runtime service must use declarative YAML instead of $variable" >&2
    exit 1
  fi
done
if printf '%s\n' "$openvpn_service" | grep -Eq '^[[:space:]]+ports:'; then
  echo 'host-networked OpenVPN service must not publish Docker ports' >&2
  exit 1
fi

printf '%s\n' "$maintenance_service" | grep -Fq 'profiles:'
printf '%s\n' "$maintenance_service" | grep -Fq -- '- maintenance'
printf '%s\n' "$maintenance_service" | grep -Fq 'entrypoint:'
printf '%s\n' "$maintenance_service" | grep -Fq -- '- /usr/local/bin/ovpn'
printf '%s\n' "$maintenance_service" | grep -Fq 'command:'
printf '%s\n' "$maintenance_service" | grep -Fq -- '- doctor'
printf '%s\n' "$maintenance_service" | grep -Fq 'restart: "no"'
printf '%s\n' "$maintenance_service" | grep -Fq 'OVPN_MAINTENANCE: "true"'
printf '%s\n' "$maintenance_service" | grep -Fq 'network_mode: host'
printf '%s\n' "$maintenance_service" | grep -Fq '/etc/openvpn-config'
if printf '%s\n' "$maintenance_service" | grep -Fq 'OVPN_BOOTSTRAP_'; then
  echo 'maintenance service must not accept initialization bootstrap variables' >&2
  exit 1
fi
for variable in OVPN_GITHUB_TOKEN HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY; do
  if printf '%s\n' "$maintenance_service" | grep -Fq "$variable:"; then
    echo "maintenance service must not expose online-update variable $variable" >&2
    exit 1
  fi
done
if printf '%s\n' "$maintenance_service" | grep -Eq '^[[:space:]]+(cap_add|devices|ports|privileged):'; then
  echo 'maintenance service must not request VPN runtime privileges' >&2
  exit 1
fi

printf 'maintenance compose smoke passed\n'
