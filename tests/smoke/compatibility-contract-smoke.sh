#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"
trap 'rm -rf "$TMP_DIR"' EXIT

cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = --version ]; then
  printf 'OpenVPN %s test-build\n' "${FAKE_OPENVPN_VERSION:-2.7.5}"
  exit 0
fi
exit 64
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
# shellcheck source=../rootfs/usr/local/lib/openvpn-container/common.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/common.sh"
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/pki.sh"
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/compatibility.sh"

test "$(ovpn_semver_normalize 02.007.0005)" = 2.7.5
test "$(ovpn_semver_compare 2.7.0 2.7.5)" = -1
test "$(ovpn_semver_compare 2.7.5 2.7.5)" = 0
test "$(ovpn_semver_compare 2.7.6 2.7.5)" = 1
if ovpn_semver_normalize 2.7 >/dev/null; then
  echo 'incomplete runtime version unexpectedly parsed' >&2
  exit 1
fi

ovpn_compatibility_load_contract
test "$OPENVPN_SUPPORTED_MIN" = 2.7.0
test "$OPENVPN_SUPPORTED_MAX_EXCLUSIVE" = 2.8.0

for version in 2.7.0 2.7.5; do
  FAKE_OPENVPN_VERSION="$version" ovpn_compatibility_runtime_supported
done
for version in 2.6.9 2.8.0 3.0.0 invalid; do
  if FAKE_OPENVPN_VERSION="$version" ovpn_compatibility_runtime_supported; then
    echo "unsupported runtime unexpectedly accepted: $version" >&2
    exit 1
  fi
done

printf 'compatibility contract smoke passed\n'
