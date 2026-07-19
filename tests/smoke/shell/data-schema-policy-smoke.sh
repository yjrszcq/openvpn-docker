#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
set -a
. "$ROOT_DIR/versions.env"
set +a

[[ "$DATA_SCHEMA" =~ ^[1-9][0-9]*$ ]]
grep -Fqx "OVPN_CURRENT_DATA_SCHEMA=$DATA_SCHEMA" \
  "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/schema.sh"
test ! -e "$ROOT_DIR/compatibility/data-schema-releases.jsonl"

for ((schema = 1; schema < DATA_SCHEMA; schema++)); do
  test -r "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/migrations/$schema-to-$((schema + 1)).sh"
done

if grep -En 'MANAGEMENT_VERSION|PLATFORM_API' \
  "$ROOT_DIR/versions.env" "$ROOT_DIR/Dockerfile" \
  "$ROOT_DIR/scripts/docker-build.sh" "$ROOT_DIR/scripts/generate-build-info.sh"; then
  echo 'image build metadata still contains a separate management/platform version' >&2
  exit 1
fi

for policy in \
  "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md" \
  "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md" \
  "$ROOT_DIR/docs/en/image-update-policy.md" \
  "$ROOT_DIR/docs/cn/image-update-policy.md"; do
  test -r "$policy"
  if grep -Eq 'MANAGEMENT_VERSION|PLATFORM_API|data-schema-releases|ovpn upgrade|--to-version|repair/\.scripts' "$policy"; then
    echo "persistent policy contains a removed online-update interface: $policy" >&2
    exit 1
  fi
done

grep -Fq 'The image is the only code delivery unit.' \
  "$ROOT_DIR/docs/en/image-update-policy.md"
grep -Fq '镜像是唯一代码交付单元' \
  "$ROOT_DIR/docs/cn/image-update-policy.md"
# shellcheck disable=SC2016 # Assert literal Markdown code spans.
grep -Fq '`N-to-N+1` migration' \
  "$ROOT_DIR/docs/en/data-schema-upgrade-policy.md"
# shellcheck disable=SC2016 # Assert literal Markdown code spans.
grep -Fq '`N-to-N+1` migration' \
  "$ROOT_DIR/docs/cn/data-schema-upgrade-policy.md"

printf 'data schema policy smoke passed\n'
