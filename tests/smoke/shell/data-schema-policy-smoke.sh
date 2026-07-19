#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$ROOT_DIR"

set -a
. "$ROOT_DIR/versions.env"
set +a

[[ "$DATA_SCHEMA" =~ ^[1-9][0-9]*$ ]]
grep -Fqx "OVPN_CURRENT_DATA_SCHEMA=$DATA_SCHEMA" \
  "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/schema.sh"
test ! -e "$ROOT_DIR/compatibility/data-schema-releases.jsonl"

# Historical command references are frozen. Unlike current documentation, their
# exact contents are an intentional compatibility record.
cat <<'EOF' | sha256sum -c -
5c5c95878150c9f297d1d3520df89b30aa85253714b9f8b7279a17e9bf566e74  docs/en/v1/commands.md
a761f1f56c5eea22b66496ada40fa3710f553f619b5c77e33fcbadbd0ed44e7d  docs/en/v1/operations.md
0bca0c77221794b6590697dded6c956b9cbc566e7775b2177ac8cbc56fbb5e5d  docs/cn/v1/commands.md
f28e39e8f83d5f772743a7c6ce514c2e1b357bb649829e165dba1b8c2c54ee36  docs/cn/v1/operations.md
d61f0134655bbe22b06e90dc395c7c0b7b8b9279edc7d0de530c201a8f18fb90  docs/en/v2/commands.md
ad314ea7f5dfed4bce0a008fa57f969f0b256bfae35420a9bef58534f8ef8c2b  docs/en/v2/operations.md
c3383632d8045831eb74311434db933ed60535e460e9f57b0808de0cee0853f1  docs/cn/v2/commands.md
9c5b4d2d27c7306c2a2d4dac391670797d00dc1f04026ee467c61c3c5e6e6562  docs/cn/v2/operations.md
EOF

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

for current_doc in \
  "$ROOT_DIR/README.md" \
  "$ROOT_DIR/README_CN.md" \
  "$ROOT_DIR/docs/en/v3/commands.md" \
  "$ROOT_DIR/docs/en/v3/operations.md" \
  "$ROOT_DIR/docs/cn/v3/commands.md" \
  "$ROOT_DIR/docs/cn/v3/operations.md"; do
  test -s "$current_doc"
  if grep -Eq 'ovpn upgrade|--to-version|OVPN_GITHUB_TOKEN|MANAGEMENT_VERSION|PLATFORM_API|signed-bundle|data-schema-releases|repair/\.scripts' "$current_doc"; then
    echo "current documentation contains a removed online-update interface: $current_doc" >&2
    exit 1
  fi
done

test ! -e "$ROOT_DIR/docs/en/management-update-policy.md"
test ! -e "$ROOT_DIR/docs/cn/management-update-policy.md"

printf 'data schema policy smoke passed\n'
