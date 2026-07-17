#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OVPN="$ROOT_DIR/rootfs/usr/local/bin/ovpn"
TMP_DIR="$(mktemp -d)"
FAKE_BIN="$TMP_DIR/bin"
SOCKET_LISTENER_PID=''
mkdir -p "$FAKE_BIN"

cleanup() {
  [ -z "$SOCKET_LISTENER_PID" ] || kill "$SOCKET_LISTENER_PID" >/dev/null 2>&1 || true
  [ -z "$SOCKET_LISTENER_PID" ] || wait "$SOCKET_LISTENER_PID" 2>/dev/null || true
  rm -rf "$TMP_DIR"
}

format_client_list_row() {
  printf '%-7s  %-7s  %-7s  %-11s  %-11s  %s\n' "$@"
}

on_error() {
  local status=$?
  printf 'client lifecycle smoke failed at line %s (exit %s)\n' "$1" "$status" >&2
  exit "$status"
}
trap 'on_error "$LINENO"' ERR
trap cleanup EXIT

cat >"$FAKE_BIN/easyrsa" <<'FAKE_EASYRSA'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$EASYRSA_PKI"
if [ -n "${FAKE_EASYRSA_LOG:-}" ]; then
  printf '%s\n' "${1:-}" >>"$FAKE_EASYRSA_LOG"
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
  build-client-full)
    name="$2"
    mkdir -p "$EASYRSA_PKI/private" "$EASYRSA_PKI/issued"
    sequence_file="$EASYRSA_PKI/.fake-client-sequence"
    if [ "${FAKE_EASYRSA_FAIL_BUILD_CLIENT:-}" = "$name" ]; then
      echo "injected Easy-RSA client issuance failure" >&2
      exit 1
    fi

    sequence=0
    [ ! -r "$sequence_file" ] || sequence="$(cat "$sequence_file")"
    sequence=$((sequence + 1))
    printf '%s\n' "$sequence" >"$sequence_file"
    printf 'FAKE CLIENT CERT %s %s\n' "$name" "$sequence" >"$EASYRSA_PKI/issued/$name.crt"
    printf 'FAKE CLIENT KEY %s %s\n' "$name" "$sequence" >"$EASYRSA_PKI/private/$name.key"
    printf 'V\t30000101000000Z\t\t01\tunknown\t/CN=%s\n' "$name" >>"$EASYRSA_PKI/index.txt"
    ;;
  revoke)
    name="$2"
    tmp="$EASYRSA_PKI/index.txt.tmp"
    found=0
    while IFS= read -r line || [ -n "$line" ]; do
      status="${line%%$'\t'*}"
      subject="${line##*$'\t'}"
      if [ "$status" = V ] && [ "$subject" = "/CN=$name" ]; then
        printf 'R\t30000101000000Z\t260101000000Z\t01\tunknown\t/CN=%s\n' "$name" >>"$tmp"
        found=1
      else
        printf '%s\n' "$line" >>"$tmp"
      fi
    done <"$EASYRSA_PKI/index.txt"
    mv "$tmp" "$EASYRSA_PKI/index.txt"
    [ "$found" -eq 1 ]
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
    printf 'OpenVPN 2.7.5 test-build\n'
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

cat >"$FAKE_BIN/socat" <<'FAKE_SOCAT'
#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
printf 'HEADER\tROUTING_TABLE\tVirtual Address\tCommon Name\tReal Address\tLast Ref\r\n'
printf 'ROUTING_TABLE\t10.88.0.2\tlaptop\t198.51.100.11:1194\tWed Jul 15 00:00:00 2026\r\n'
printf 'ROUTING_TABLE\t10.88.0.200\tonline\t198.51.100.10:1194\tWed Jul 15 00:00:00 2026\r\n'
printf 'END\r\n'
FAKE_SOCAT
chmod +x "$FAKE_BIN/socat"

cat >"$FAKE_BIN/nano" <<'FAKE_NANO'
#!/usr/bin/env bash
set -euo pipefail
printf 'nano:%s\n' "${1##*/}" >>"${OVPN_TEST_EDITOR_LOG:?}"
FAKE_NANO
chmod +x "$FAKE_BIN/nano"

export OVPN_LIB_DIR="$ROOT_DIR/rootfs/usr/local/lib/openvpn-container"
export OVPN_TEMPLATE_ROOT="$ROOT_DIR/rootfs/usr/local/share/openvpn-container/templates"
export OVPN_COMPATIBILITY_DIR="$ROOT_DIR/compatibility"
export OVPN_DATA_DIR="$TMP_DIR/openvpn"
export OVPN_RUNTIME_DIR="$TMP_DIR/run"
export OVPN_MANAGEMENT_SOCKET="$OVPN_RUNTIME_DIR/management.sock"
export OVPN_LEASE_DIR="$TMP_DIR/runtime/leases"
export OVPN_SOCAT_BIN="$FAKE_BIN/socat"
export OVPN_ENDPOINT="vpn.example.test"
export OVPN_NETWORK="10.88.0.0/24"
export OVPN_EASYRSA_BIN="$FAKE_BIN/easyrsa"
export OVPN_OPENVPN_BIN="$FAKE_BIN/openvpn"
export OVPN_OPENSSL_BIN="$ROOT_DIR/tests/helpers/fake-openssl.sh"

"$OVPN" init >/tmp/ovpn-client-init.out 2>/tmp/ovpn-client-init.err
"$OVPN" client create laptop >/tmp/ovpn-add-client.out 2>/tmp/ovpn-add-client.err

repair_snapshot() {
  find "$OVPN_DATA_DIR" \
    \( -path "$OVPN_DATA_DIR/repair" -o -name .ovpn-data.lock \) -prune -o \
    -type f -print0 | sort -z | xargs -0 sha256sum
}

identity_before="$(sha256sum \
  "$OVPN_DATA_DIR/pki/ca.crt" \
  "$OVPN_DATA_DIR/pki/private/ca.key" \
  "$OVPN_DATA_DIR/pki/issued/openvpn-server.crt" \
  "$OVPN_DATA_DIR/pki/private/openvpn-server.key" \
  "$OVPN_DATA_DIR/pki/issued/laptop.crt" \
  "$OVPN_DATA_DIR/pki/private/laptop.key")"
rm "$OVPN_DATA_DIR/config/schema-version" \
  "$OVPN_DATA_DIR/meta/instance.json" \
  "$OVPN_DATA_DIR/server/server.conf" \
  "$OVPN_DATA_DIR/pki/crl.pem" \
  "$OVPN_DATA_DIR/clients/active/laptop.ovpn"
rm -rf "$OVPN_RUNTIME_DIR"
"$OVPN" repair apply >"$TMP_DIR/repair.out" 2>"$TMP_DIR/repair.err"
if [ "$("$OVPN" state show)" != HEALTHY ]; then
  echo 'safe repair did not restore HEALTHY state' >&2
  exit 1
fi
[ -d "$OVPN_RUNTIME_DIR" ]
test -f "$OVPN_DATA_DIR/config/schema-version"
test -f "$OVPN_DATA_DIR/meta/instance.json"
test -f "$OVPN_DATA_DIR/server/server.conf"
test -f "$OVPN_DATA_DIR/pki/crl.pem"
test -f "$OVPN_DATA_DIR/clients/active/laptop.ovpn"
identity_after="$(sha256sum \
  "$OVPN_DATA_DIR/pki/ca.crt" \
  "$OVPN_DATA_DIR/pki/private/ca.key" \
  "$OVPN_DATA_DIR/pki/issued/openvpn-server.crt" \
  "$OVPN_DATA_DIR/pki/private/openvpn-server.key" \
  "$OVPN_DATA_DIR/pki/issued/laptop.crt" \
  "$OVPN_DATA_DIR/pki/private/laptop.key")"
[ "$identity_before" = "$identity_after" ] || {
  echo 'safe repair changed identity material' >&2
  exit 1
}
grep -Fq 'completed 6 automatic repair actions' "$TMP_DIR/repair.err"

rm "$OVPN_DATA_DIR/server/server.conf"
export FAKE_OPENSSL_LOG="$TMP_DIR/repair-lock-openssl.log"
export FAKE_OPENSSL_SLEEP_ON=x509
export FAKE_OPENSSL_SLEEP_SECONDS=1
export FAKE_EASYRSA_LOG="$TMP_DIR/repair-lock-easyrsa.log"
: >"$FAKE_OPENSSL_LOG"
: >"$FAKE_EASYRSA_LOG"
"$OVPN" repair apply >"$TMP_DIR/locked-repair.out" 2>"$TMP_DIR/locked-repair.err" &
repair_pid=$!
deadline=$((SECONDS + 5))
while ! grep -Fqx x509 "$FAKE_OPENSSL_LOG"; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    wait "$repair_pid" || true
    echo 'repair did not reach the shared lock' >&2
    exit 1
  fi
  sleep 0.1
done
"$OVPN" client create tablet >"$TMP_DIR/locked-add-client.out" 2>"$TMP_DIR/locked-add-client.err" &
add_client_pid=$!
sleep 0.1
if grep -Fqx build-client-full "$FAKE_EASYRSA_LOG"; then
  echo 'add-client bypassed the repair data lock' >&2
  exit 1
fi
wait "$repair_pid"
wait "$add_client_pid"
grep -Fqx build-client-full "$FAKE_EASYRSA_LOG"
unset FAKE_OPENSSL_LOG FAKE_OPENSSL_SLEEP_ON FAKE_OPENSSL_SLEEP_SECONDS FAKE_EASYRSA_LOG
if [ "$("$OVPN" state show)" != HEALTHY ]; then
  echo 'shared-lock repair and client mutation did not leave HEALTHY state' >&2
  exit 1
fi
rm "$OVPN_DATA_DIR/server/server.conf" "$OVPN_DATA_DIR/clients/active/laptop.ovpn"
before_failed_repair="$(repair_snapshot)"
if OVPN_REPAIR_FAIL_AFTER_INSTALL=RENDER_SERVER_CONFIG "$OVPN" repair apply >"$TMP_DIR/failed-repair.out" 2>"$TMP_DIR/failed-repair.err"; then
  echo 'injected repair failure unexpectedly succeeded' >&2
  exit 1
fi
after_failed_repair="$(repair_snapshot)"
[ "$before_failed_repair" = "$after_failed_repair" ] || {
  echo 'failed repair did not roll back persisted targets' >&2
  exit 1
}
test ! -e "$OVPN_DATA_DIR/server/server.conf"
test ! -e "$OVPN_DATA_DIR/clients/active/laptop.ovpn"
if compgen -G "$OVPN_DATA_DIR/repair/.stage-*" >/dev/null; then
  echo 'failed repair left a staging directory' >&2
  exit 1
fi
journal="$(grep -rl -- '"result": "failed"' "$OVPN_DATA_DIR/repair/journal" | head -n 1)"
[ -n "$journal" ] || {
  echo 'failed repair did not create a journal' >&2
  exit 1
}
if grep -Fq 'FAKE CLIENT KEY laptop' "$journal"; then
  echo 'repair journal contains private profile material' >&2
  exit 1
fi
"$OVPN" repair apply >"$TMP_DIR/retry-repair.out" 2>"$TMP_DIR/retry-repair.err"
if [ "$("$OVPN" state show)" != HEALTHY ]; then
  echo 'retry after failed repair did not restore HEALTHY state' >&2
  exit 1
fi


grep -E '^laptop[[:space:]]+active$' <("$OVPN" client list)
test -f "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

"$OVPN" client create phone --dynamic >"$TMP_DIR/phone-create.out" 2>"$TMP_DIR/phone-create.err"
grep -Fqx 'phone,' "$OVPN_DATA_DIR/data/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/phone"
"$OVPN" client ip set phone --ip 10.88.0.20 >"$TMP_DIR/phone-static.out" 2>"$TMP_DIR/phone-static.err"
grep -Fqx 'phone,10.88.0.20' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'ifconfig-push 10.88.0.20 255.255.255.0' "$OVPN_DATA_DIR/ccd/phone"
"$OVPN" client ip set laptop --dynamic >"$TMP_DIR/laptop-dynamic.out" 2>"$TMP_DIR/laptop-dynamic.err"
grep -Fqx 'laptop,' "$OVPN_DATA_DIR/data/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/laptop"
env -u OVPN_EDITOR -u EDITOR \
  OVPN_TEST_EDITOR_LOG="$TMP_DIR/editor.log" \
  PATH="$FAKE_BIN:$PATH" \
  "$OVPN" client ip set laptop phone >"$TMP_DIR/batch-static.out" 2>"$TMP_DIR/batch-static.err"
grep -Eq '^nano:\.client-ip-set\.' "$TMP_DIR/editor.log"
grep -Fqx 'laptop,10.88.0.2' "$OVPN_DATA_DIR/data/client-ip.csv"
grep -Fqx 'ifconfig-push 10.88.0.2 255.255.255.0' "$OVPN_DATA_DIR/ccd/laptop"

mkdir -p "$OVPN_RUNTIME_DIR"
nc -lU "$OVPN_MANAGEMENT_SOCKET" >/dev/null 2>&1 &
SOCKET_LISTENER_PID=$!
for attempt in {1..20}; do
  [ -S "$OVPN_MANAGEMENT_SOCKET" ] && break
  sleep 0.1
done
[ -S "$OVPN_MANAGEMENT_SOCKET" ] || {
  echo 'failed to create a UNIX management socket fixture' >&2
  exit 1
}

"$OVPN" client create old --ip 10.88.0.30 >"$TMP_DIR/old-create.out" 2>"$TMP_DIR/old-create.err"
"$OVPN" client revoke old >"$TMP_DIR/old-revoke.out" 2>"$TMP_DIR/old-revoke.err"
"$OVPN" client create online --dynamic >"$TMP_DIR/online-create.out" 2>"$TMP_DIR/online-create.err"
"$OVPN" client create known --dynamic >"$TMP_DIR/known-create.out" 2>"$TMP_DIR/known-create.err"
"$OVPN" client create missing --dynamic >"$TMP_DIR/missing-create.out" 2>"$TMP_DIR/missing-create.err"
mkdir -p "$OVPN_LEASE_DIR"
printf '10.88.0.201\n' >"$OVPN_LEASE_DIR/online"
printf '10.88.0.202\n' >"$OVPN_LEASE_DIR/known"
printf '10.88.0.203\n' >"$OVPN_LEASE_DIR/unrelated"
"$OVPN" client list --detail >"$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row CLIENT STATE MODE IP 'IP STATE' CONNECTION)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row laptop active static 10.88.0.2 configured online)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row phone active static 10.88.0.20 configured offline)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row old revoked static 10.88.0.30 retained offline)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row online active dynamic 10.88.0.200 connected online)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row known active dynamic 10.88.0.202 last-known offline)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row missing active dynamic - unavailable offline)" "$TMP_DIR/client-list-ip.out"
if grep -Fq 'unrelated' "$TMP_DIR/client-list-ip.out"; then
  echo 'client IP list included a lease for an unknown client' >&2
  exit 1
fi
grep -E '^laptop[[:space:]]+active$' <("$OVPN" client list)
grep -Fqx "$(format_client_list_row online active dynamic 10.88.0.200 connected online)" <("$OVPN" client list --detail)
rm -f "$OVPN_MANAGEMENT_SOCKET"
"$OVPN" client list --detail >"$TMP_DIR/client-list-ip-unknown.out"
grep -Fqx "$(format_client_list_row laptop active static 10.88.0.2 configured unknown)" "$TMP_DIR/client-list-ip-unknown.out"
grep -Fqx "$(format_client_list_row online active dynamic 10.88.0.201 last-known unknown)" "$TMP_DIR/client-list-ip-unknown.out"

"$OVPN" client export laptop >"$TMP_DIR/laptop.ovpn" 2>"$TMP_DIR/export.err"
test ! -s "$TMP_DIR/export.err"
grep -q '^remote vpn.example.test 1194$' "$TMP_DIR/laptop.ovpn"
grep -q 'FAKE CLIENT CERT laptop' "$TMP_DIR/laptop.ovpn"
grep -q 'FAKE CLIENT KEY laptop' "$TMP_DIR/laptop.ovpn"

OVPN_ENDPOINT=changed.example.test \
  OVPN_PROTO=tcp \
  OVPN_PORT=443 \
  OVPN_NETWORK=10.88.0.0/24 \
  OVPN_NAT=true \
  OVPN_NAT_INTERFACE=auto \
  OVPN_REDIRECT_GATEWAY=false \
  OVPN_CLIENT_TO_CLIENT=false \
  OVPN_DNS='' \
  OVPN_ROUTES='' \
  "$OVPN" config apply
"$OVPN" client export laptop >"$TMP_DIR/laptop-updated.ovpn" 2>"$TMP_DIR/export-updated.err"
test ! -s "$TMP_DIR/export-updated.err"
grep -q '^remote changed.example.test 443$' "$TMP_DIR/laptop-updated.ovpn"
grep -q '^proto tcp$' "$TMP_DIR/laptop-updated.ovpn"
cmp "$TMP_DIR/laptop-updated.ovpn" "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

rm "$OVPN_DATA_DIR/clients/active/laptop.ovpn"
if "$OVPN" client export laptop >"$TMP_DIR/missing-profile-export.out" 2>"$TMP_DIR/missing-profile-export.err"; then
  echo 'missing profile export unexpectedly succeeded' >&2
  exit 1
fi
grep -q 'DEGRADED_REPAIRABLE' "$TMP_DIR/missing-profile-export.err"
"$OVPN" repair apply >"$TMP_DIR/missing-profile-repair.out" 2>"$TMP_DIR/missing-profile-repair.err"
grep -q '^remote changed.example.test 443$' "$OVPN_DATA_DIR/clients/active/laptop.ovpn"
grep -q '^proto tcp$' "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

if "$OVPN" client create laptop >"$TMP_DIR/duplicate.out" 2>"$TMP_DIR/duplicate.err"; then
  echo 'duplicate add-client unexpectedly succeeded' >&2
  exit 1
fi

grep -q 'already exists' "$TMP_DIR/duplicate.err"

"$OVPN" client revoke phone --release-ip >"$TMP_DIR/phone-revoke.out" 2>"$TMP_DIR/phone-revoke.err"
grep -E '^phone[[:space:]]+revoked$' <("$OVPN" client list)
grep -Fqx 'phone,' "$OVPN_DATA_DIR/data/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/phone"
test -f "$OVPN_DATA_DIR/clients/revoked/phone.ovpn"
phone_key_before="$(sha256sum "$OVPN_DATA_DIR/pki/private/phone.key")"
"$OVPN" client reissue phone >"$TMP_DIR/phone-reissue.out" 2>"$TMP_DIR/phone-reissue.err"
phone_key_after="$(sha256sum "$OVPN_DATA_DIR/pki/private/phone.key")"
[ "$phone_key_before" != "$phone_key_after" ] || {
  echo 'reissue did not generate a new private key' >&2
  exit 1
}
grep -E '^phone[[:space:]]+active$' <("$OVPN" client list)
grep -q '^phone,10\.88\.0\.' "$OVPN_DATA_DIR/data/client-ip.csv" || {
  echo 'reissue did not auto-allocate a static IP for a client with no IP' >&2
  exit 1
}
test -f "$OVPN_DATA_DIR/clients/active/phone.ovpn"
"$OVPN" client delete phone >"$TMP_DIR/phone-delete.out" 2>"$TMP_DIR/phone-delete.err"
if grep -q '^phone,' "$OVPN_DATA_DIR/data/client-ip.csv"; then
  echo 'deleted client remained in the IP registry' >&2
  exit 1
fi
grep -Fqx 'phone,deleted' "$OVPN_DATA_DIR/meta/client-state.csv"
test ! -e "$OVPN_DATA_DIR/pki/private/phone.key"
test ! -e "$OVPN_DATA_DIR/clients/active/phone.ovpn"

tablet_index_before="$(sha256sum "$OVPN_DATA_DIR/pki/index.txt")"
if FAKE_EASYRSA_FAIL_BUILD_CLIENT=tablet "$OVPN" client reissue tablet >"$TMP_DIR/tablet-reissue.out" 2>"$TMP_DIR/tablet-reissue.err"; then
  echo 'unsupported same-CN reissue unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'does not support same-CN reissue' "$TMP_DIR/tablet-reissue.err"
tablet_index_after="$(sha256sum "$OVPN_DATA_DIR/pki/index.txt")"
[ "$tablet_index_before" = "$tablet_index_after" ] || {
  echo 'unsupported reissue modified the PKI index' >&2
  exit 1
}

if "$OVPN" client ip release laptop >"$TMP_DIR/active-release-ip.out" 2>"$TMP_DIR/active-release-ip.err"; then
  echo "active client IP release unexpectedly succeeded" >&2
  exit 1
fi
grep -Fq "is not revoked" "$TMP_DIR/active-release-ip.err"

"$OVPN" client revoke laptop >"$TMP_DIR/revoke.out" 2>"$TMP_DIR/revoke.err"
grep -E "^laptop[[:space:]]+revoked$" <("$OVPN" client list)
grep -Fqx "laptop,10.88.0.2" "$OVPN_DATA_DIR/data/client-ip.csv"
test -f "$OVPN_DATA_DIR/clients/revoked/laptop.ovpn"
test ! -e "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

"$OVPN" client ip release laptop >"$TMP_DIR/release-ip.out" 2>"$TMP_DIR/release-ip.err"
grep -Fqx "laptop," "$OVPN_DATA_DIR/data/client-ip.csv"
grep -E "^laptop[[:space:]]+revoked$" <("$OVPN" client list)
test ! -e "$OVPN_DATA_DIR/ccd/laptop"
test -f "$OVPN_DATA_DIR/clients/revoked/laptop.ovpn"
test -f "$OVPN_DATA_DIR/pki/private/laptop.key"
grep -Fq release_ip "$OVPN_DATA_DIR/meta/audit.jsonl"
if "$OVPN" client ip release laptop >"$TMP_DIR/repeated-release-ip.out" 2>"$TMP_DIR/repeated-release-ip.err"; then
  echo "repeated client IP release unexpectedly succeeded" >&2
  exit 1
fi
grep -Fq "does not have a static IP reservation" "$TMP_DIR/repeated-release-ip.err"

if "$OVPN" client export laptop >"$TMP_DIR/revoked-export.out" 2>"$TMP_DIR/revoked-export.err"; then
  echo "revoked client export unexpectedly succeeded" >&2
  exit 1
fi

grep -q "is revoked" "$TMP_DIR/revoked-export.err"

printf 'client lifecycle smoke passed\n'
