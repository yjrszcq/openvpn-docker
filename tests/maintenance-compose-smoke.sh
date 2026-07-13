#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  printf 'maintenance compose smoke skipped: docker compose is unavailable\n'
  exit 0
fi

docker compose --profile maintenance -f "$ROOT_DIR/docker-compose.example.yaml" config >"$TMP_DIR/compose.yaml"
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
if printf '%s\n' "$maintenance_service" | grep -Eq '^[[:space:]]+(cap_add|devices|ports|privileged):'; then
  echo 'maintenance service must not request VPN runtime privileges' >&2
  exit 1
fi

printf 'maintenance compose smoke passed\n'
