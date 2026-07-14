#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EN_README="$ROOT_DIR/README.md"
CN_README="$ROOT_DIR/README_CN.md"
COMPOSE_EXAMPLE="$ROOT_DIR/docker-compose.example.yaml"
ENV_EXAMPLE="$ROOT_DIR/.env.example"

for manual in "$EN_README" "$CN_README"; do
  test -s "$manual"
done

grep -Fq '## Client IP Management' "$EN_README"
grep -Fq 'data/client-ip.csv is the sole IP-assignment fact' "$EN_README"
grep -Fq 'ovpn client-ip apply' "$EN_README"
grep -Fq 'waiting for explicit application' "$EN_README"
grep -Fq 'ovpn network reconfigure' "$EN_README"
grep -Fq -- '--dry-run' "$EN_README"
grep -Fq -- '--yes' "$EN_README"
grep -Fq './openvpn-runtime/pool-persist.txt' "$EN_README"
grep -Fq '2^(32 - p) - 3' "$EN_README"
grep -Fq '10.42.0.0/24` provides 253 client' "$EN_README"

grep -Fq '## 客户端 IP 管理' "$CN_README"
grep -Fq 'data/client-ip.csv 是唯一的 IP 分配事实源' "$CN_README"
grep -Fq 'ovpn client-ip apply' "$CN_README"
grep -Fq '等待显式应用' "$CN_README"
grep -Fq 'ovpn network reconfigure' "$CN_README"
grep -Fq './openvpn-runtime/pool-persist.txt' "$CN_README"
grep -Fq '2^(32-p)-3' "$CN_README"
grep -Fq '10.42.0.0/24` 可提供 253 个' "$CN_README"

grep -Fq './openvpn-runtime:/var/lib/openvpn' "$COMPOSE_EXAMPLE"
grep -Fq 'OVPN_TOPOLOGY=subnet' "$ENV_EXAMPLE"
grep -Fq 'OVPN_DYNAMIC_POOL_SIZE=64' "$ENV_EXAMPLE"

printf 'IPAM documentation smoke passed\n'
