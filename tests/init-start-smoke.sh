#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"

cat >"$FAKE_BIN/easyrsa" <<'FAKE_EASYRSA'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$EASYRSA_PKI"
if [ -n "${FAKE_EASYRSA_LOG:-}" ]; then
  printf '%s\n' "${1:-}" >>"$FAKE_EASYRSA_LOG"
fi
if [ "${FAKE_EASYRSA_SLEEP_ON:-}" = "${1:-}" ]; then
  sleep "${FAKE_EASYRSA_SLEEP_SECONDS:-1}"
fi
case "${1:-}" in
  init-pki)
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued" "$EASYRSA_PKI/reqs" "$EASYRSA_PKI/revoked" "$EASYRSA_PKI/certs_by_serial"
    : >"$EASYRSA_PKI/index.txt"
    printf '01\n' >"$EASYRSA_PKI/serial"
    ;;
  build-ca)
    mkdir -p "$EASYRSA_PKI/private"
    printf 'FAKE CA CERT\n' >"$EASYRSA_PKI/ca.crt"
    printf 'FAKE CA KEY\n' >"$EASYRSA_PKI/private/ca.key"
    ;;
  build-server-full)
    name="$2"
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued"
    printf 'FAKE SERVER CERT\n' >"$EASYRSA_PKI/issued/$name.crt"
    printf 'FAKE SERVER KEY\n' >"$EASYRSA_PKI/private/$name.key"
    ;;
  gen-crl)
    printf 'FAKE CRL\n' >"$EASYRSA_PKI/crl.pem"
    ;;
  *)
    echo "unexpected easyrsa command: $*" >&2
    exit 1
    ;;
esac
FAKE_EASYRSA
chmod +x "$FAKE_BIN/easyrsa"

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
  --genkey)
    printf 'FAKE TLS CRYPT KEY\n' >"$3"
    ;;
  *)
    printf 'fake-openvpn %s\n' "$*"
    ;;
esac
FAKE_OPENVPN
chmod +x "$FAKE_BIN/openvpn"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_EASYRSA_BIN="$FAKE_BIN/easyrsa"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"

if [ "$("$OVPN" state)" != EMPTY ]; then
  echo 'fresh data dir should be EMPTY' >&2
  exit 1
fi

"$OVPN" start >"$TMP_DIR/empty-start.out" 2>"$TMP_DIR/empty-start.err"
if [ "$("$OVPN" state)" != HEALTHY ]; then
  echo 'auto-initialized data dir should be HEALTHY' >&2
  exit 1
fi
grep -q 'initialized OpenVPN data directory' "$TMP_DIR/empty-start.err"
grep -q -- "--config $OVPN_DATA_DIR/server/server.conf" "$TMP_DIR/empty-start.out"

grep -q 'vpn.example.test' "$OVPN_DATA_DIR/config/project.env"
grep -q '^server 10.88.0.0 255.255.255.0$' "$OVPN_DATA_DIR/server/server.conf"
rm "$OVPN_DATA_DIR/server/server.conf"
"$OVPN" start >"$TMP_DIR/auto-repair-start.out" 2>"$TMP_DIR/auto-repair-start.err"
if [ "$("$OVPN" state)" != HEALTHY ]; then
  echo 'repairable start did not restore HEALTHY state' >&2
  exit 1
fi
grep -Fq 'applying automatic repairs' "$TMP_DIR/auto-repair-start.err"
test -f "$OVPN_DATA_DIR/server/server.conf"

export OVPN_DATA_DIR="$TMP_DIR/missing-endpoint"
set +e
OVPN_ENDPOINT= "$OVPN" start >"$TMP_DIR/missing-endpoint.out" 2>"$TMP_DIR/missing-endpoint.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'start unexpectedly initialized without an endpoint' >&2
  exit 1
fi
grep -q 'OVPN_ENDPOINT must be a hostname or IP address' "$TMP_DIR/missing-endpoint.err"
if [ "$("$OVPN" state)" != EMPTY ]; then
  echo 'missing endpoint left non-empty data' >&2
  exit 1
fi
test ! -e "$OVPN_DATA_DIR/pki/ca.key"

export OVPN_DATA_DIR="$TMP_DIR/partial"
mkdir -p "$OVPN_DATA_DIR/pki"
set +e
"$OVPN" start >"$TMP_DIR/partial-start.out" 2>"$TMP_DIR/partial-start.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'start unexpectedly initialized partial data' >&2
  exit 1
fi
grep -q 'instance state is UNRECOVERABLE; refusing to start' "$TMP_DIR/partial-start.err"
test ! -e "$OVPN_DATA_DIR/pki/private/ca.key"

export OVPN_DATA_DIR="$TMP_DIR/openvpn"

grep -q 'vpn.example.test' "$OVPN_DATA_DIR/config/project.env"
grep -q '^server 10.88.0.0 255.255.255.0$' "$OVPN_DATA_DIR/server/server.conf"
set +e
FAKE_OPENVPN_MISSING_FEATURE=tls-crypt "$OVPN" start >"$TMP_DIR/missing-capability-start.out" 2>"$TMP_DIR/missing-capability-start.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'start unexpectedly accepted a runtime missing a required capability' >&2
  exit 1
fi
grep -q 'lacks required capabilities' "$TMP_DIR/missing-capability-start.err"

grep -q "$OVPN_DATA_DIR/pki/ca.crt" "$OVPN_DATA_DIR/server/server.conf"
test -f "$OVPN_DATA_DIR/meta/instance.json"
test -f "$OVPN_DATA_DIR/secrets/tls-crypt.key"

"$OVPN" start >"$TMP_DIR/start.out" 2>"$TMP_DIR/start.err"
grep -q -- "--config $OVPN_DATA_DIR/server/server.conf" "$TMP_DIR/start.out"
set +e
FAKE_OPENVPN_VERSION=2.8.0 "$OVPN" start >"$TMP_DIR/unsupported-start.out" 2>"$TMP_DIR/unsupported-start.err"
status=$?
set -e
if [ "$status" -eq 0 ]; then
  echo 'start unexpectedly accepted an unsupported runtime' >&2
  exit 1
fi
grep -q 'outside supported range' "$TMP_DIR/unsupported-start.err"


export OVPN_DATA_DIR="$TMP_DIR/concurrent"
export FAKE_EASYRSA_LOG="$TMP_DIR/concurrent-easyrsa.log"
export FAKE_EASYRSA_SLEEP_ON=build-ca
export FAKE_EASYRSA_SLEEP_SECONDS=1
: >"$FAKE_EASYRSA_LOG"
"$OVPN" start >"$TMP_DIR/concurrent-first.out" 2>"$TMP_DIR/concurrent-first.err" &
first_pid=$!
deadline=$((SECONDS + 5))
while ! grep -Fqx build-ca "$FAKE_EASYRSA_LOG"; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    wait "$first_pid" || true
    echo 'first concurrent start did not reach PKI generation' >&2
    exit 1
  fi
  sleep 0.1
done
"$OVPN" start >"$TMP_DIR/concurrent-second.out" 2>"$TMP_DIR/concurrent-second.err" &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
build_ca_count="$(grep -Fxc build-ca "$FAKE_EASYRSA_LOG" || true)"
if [ "$build_ca_count" -ne 1 ]; then
  echo "concurrent starts created $build_ca_count certificate authorities" >&2
  exit 1
fi
if [ "$("$OVPN" state)" != HEALTHY ]; then
  echo 'concurrent starts did not leave a HEALTHY data directory' >&2
  exit 1
fi
test -f "$OVPN_DATA_DIR/pki/private/ca.key"
test ! -e "$OVPN_DATA_DIR/.init-transaction"
unset FAKE_EASYRSA_LOG FAKE_EASYRSA_SLEEP_ON FAKE_EASYRSA_SLEEP_SECONDS

printf 'init/start smoke passed\n'
