#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
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

test "$(ovpn_compatibility_adapter_name)" = openvpn-2.7
test "$(ovpn_compatibility_template_family)" = openvpn-2.7
test -r "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates/$(ovpn_compatibility_template_family)/server.conf.tpl"
test -r "$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates/$(ovpn_compatibility_template_family)/client.ovpn.tpl"

if FAKE_OPENVPN_VERSION=2.7.6 ovpn_compatibility_template_family >/dev/null; then
  echo 'unverified runtime unexpectedly selected a template family' >&2
  exit 1
fi

bad_contract="$TMP_DIR/contract.env"
sed 's/^OPENVPN_ADAPTER=.*/OPENVPN_ADAPTER=missing-adapter/' "$ROOT_DIR/compatibility/contract.env" >"$bad_contract"
OVPN_COMPATIBILITY_CONTRACT="$bad_contract"
if ovpn_compatibility_template_family >/dev/null; then
  echo 'missing adapter unexpectedly selected a template family' >&2
  exit 1
fi

printf 'adapter selection smoke passed\n'
