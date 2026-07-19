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
f98d44cd721060984e5fc7f37c89895101500770bd7e9d47a9a512d5606fde99  docs/en/v1/commands.md
e22252a34a7b53313710b71558dc6f50e6416824b280b0925d5e3c85a94c35d1  docs/en/v1/operations.md
0bca0c77221794b6590697dded6c956b9cbc566e7775b2177ac8cbc56fbb5e5d  docs/cn/v1/commands.md
22515bc3c5b64819f1842998805887d8dadcd9f5a49871d8d721b4a03774c3d8  docs/cn/v1/operations.md
b58589fe0683cd166b26f13bcfada8446b7dae54b0a40d225776e8468f9577ff  docs/en/v2/commands.md
d13cee8fe91ca5a8d68afa2c105333bfb7d419ce23bcbebc04f063e1bda67edb  docs/en/v2/operations.md
c3383632d8045831eb74311434db933ed60535e460e9f57b0808de0cee0853f1  docs/cn/v2/commands.md
651c956ce94c5af13daf446a665694c5f37d9a9f0e8154326055f5deb9f2f973  docs/cn/v2/operations.md
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
