#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"
trap 'rm -rf "$TMP_DIR"' EXIT

cat >"$FAKE_BIN/openvpn" <<'FAKE_OPENVPN'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  --version)
    printf 'OpenVPN %s test-build\n' "${FAKE_OPENVPN_VERSION:-2.7.5}"
    ;;
  --help)
    if [ "${FAKE_OPENVPN_MISSING_FEATURE:-}" != tls-crypt ]; then
      printf '%s\n' '--tls-crypt key'
    fi
    if [ "${FAKE_OPENVPN_MISSING_FEATURE:-}" != data-ciphers ]; then
      printf '%s\n' '--data-ciphers list'
    fi
    if [ "${FAKE_OPENVPN_MISSING_FEATURE:-}" != crl-verify ]; then
      printf '%s\n' '--crl-verify crl'
    fi
    if [ "${FAKE_OPENVPN_MISSING_FEATURE:-}" != topology-subnet ]; then
      printf '%s\n' "--topology t: 'net30', 'p2p', or 'subnet'"
    fi
    exit 1
    ;;
  --config)
    test "${3:-}" = --cipher
    test "${4:-}" = AES-256-GCM
    test "${5:-}" = --test-crypto
    ;;
  *)
    exit 64
    ;;
esac
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
# shellcheck source=../rootfs/usr/local/lib/openvpn-container/common.sh
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/common.sh"
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/pki.sh"
. "$ROOT_DIR/rootfs/usr/local/lib/openvpn-container/compatibility.sh"
: >"$TMP_DIR/generated.conf"
ovpn_compatibility_validate_config "$TMP_DIR/generated.conf"

"$OVPN" capabilities >"$TMP_DIR/supported.json"
grep -Fq '"openvpn_version": "2.7.5"' "$TMP_DIR/supported.json"
grep -Fq '"supported_range": true' "$TMP_DIR/supported.json"
grep -Fq '"adapter": "openvpn-2.7"' "$TMP_DIR/supported.json"
grep -Fq '"tls_crypt": true' "$TMP_DIR/supported.json"
grep -Fq '"data_ciphers": true' "$TMP_DIR/supported.json"
grep -Fq '"crl_verify": true' "$TMP_DIR/supported.json"
grep -Fq '"topology_subnet": true' "$TMP_DIR/supported.json"

set +e
FAKE_OPENVPN_MISSING_FEATURE=tls-crypt "$OVPN" capabilities >"$TMP_DIR/missing-feature.json" 2>"$TMP_DIR/missing-feature.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'missing required feature unexpectedly passed capabilities' >&2
  exit 1
fi
grep -Fq '"supported_range": true' "$TMP_DIR/missing-feature.json"
grep -Fq '"adapter": "openvpn-2.7"' "$TMP_DIR/missing-feature.json"
grep -Fq '"tls_crypt": false' "$TMP_DIR/missing-feature.json"

set +e
FAKE_OPENVPN_VERSION=2.8.0 "$OVPN" capabilities >"$TMP_DIR/unsupported-runtime.json" 2>"$TMP_DIR/unsupported-runtime.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'unsupported runtime unexpectedly passed capabilities' >&2
  exit 1
fi
grep -Fq '"openvpn_version": "2.8.0"' "$TMP_DIR/unsupported-runtime.json"
grep -Fq '"supported_range": false' "$TMP_DIR/unsupported-runtime.json"
grep -Fq '"adapter": null' "$TMP_DIR/unsupported-runtime.json"
grep -Fq '"tls_crypt": false' "$TMP_DIR/unsupported-runtime.json"

printf 'capabilities smoke passed\n'
