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
  printf '%-12s  %-7s  %-7s  %-7s  %-11s  %-11s  %s\n' "$@"
}

format_client_list_full_row() {
  printf '%-36s  %-7s  %-7s  %-7s  %-11s  %-11s  %s\n' "$@"
}

short_client_id() {
  local compact="${1//-/}"
  printf '%s\n' "${compact:0:12}"
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
    if [ "${FAKE_EASYRSA_FAIL_BUILD_CLIENT_AFTER_OUTPUT:-}" = "$name" ] && \
      [[ "$EASYRSA_PKI" == */.pki-operation.*/pki ]]; then
      echo "injected Easy-RSA post-issuance failure" >&2
      exit 1
    fi
    printf 'V\t30000101000000Z\t\t01\tunknown\t/CN=%s\n' "$name" >>"$EASYRSA_PKI/index.txt"
    ;;
  revoke)
    name="$2"
    if [ "${FAKE_EASYRSA_FAIL_REVOKE:-}" = "$name" ]; then
      echo "injected Easy-RSA revoke failure" >&2
      exit 1
    fi
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
    if [ "${FAKE_EASYRSA_FAIL_GEN_CRL:-false}" = true ] && \
      [[ "$EASYRSA_PKI" == */.pki-operation.*/pki ]]; then
      echo "injected Easy-RSA CRL generation failure" >&2
      exit 1
    fi
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
printf 'ROUTING_TABLE\t10.88.0.2\t%s\t198.51.100.11:1194\tWed Jul 15 00:00:00 2026\r\n' "${TEST_LAPTOP_ID:-unknown}"
printf 'ROUTING_TABLE\t10.88.0.200\t%s\t198.51.100.10:1194\tWed Jul 15 00:00:00 2026\r\n' "${TEST_ONLINE_ID:-unknown}"
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
laptop_id="$(awk -F, '$2 == "laptop" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
[[ "$laptop_id" =~ ^[0-9a-f-]{36}$ ]]
export TEST_LAPTOP_ID="$laptop_id"

repair_snapshot() {
  find "$OVPN_DATA_DIR" \
    \( -path "$OVPN_DATA_DIR/repair" -o -name .ovpn-data.lock \) -prune -o \
    -type f -print0 | sort -z | xargs -0 sha256sum
}

pki_snapshot() {
  find "$OVPN_DATA_DIR/pki" -type f -print0 | sort -z | xargs -0 sha256sum
}

identity_before="$(sha256sum \
  "$OVPN_DATA_DIR/pki/ca.crt" \
  "$OVPN_DATA_DIR/pki/private/ca.key" \
  "$OVPN_DATA_DIR/pki/issued/openvpn-server.crt" \
  "$OVPN_DATA_DIR/pki/private/openvpn-server.key" \
  "$OVPN_DATA_DIR/pki/issued/$laptop_id.crt" \
  "$OVPN_DATA_DIR/pki/private/$laptop_id.key")"
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
  "$OVPN_DATA_DIR/pki/issued/$laptop_id.crt" \
  "$OVPN_DATA_DIR/pki/private/$laptop_id.key")"
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
tablet_id="$(awk -F, '$2 == "tablet" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
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
if grep -Fq "FAKE CLIENT KEY $laptop_id" "$journal"; then
  echo 'repair journal contains private profile material' >&2
  exit 1
fi
"$OVPN" repair apply >"$TMP_DIR/retry-repair.out" 2>"$TMP_DIR/retry-repair.err"
if [ "$("$OVPN" state show)" != HEALTHY ]; then
  echo 'retry after failed repair did not restore HEALTHY state' >&2
  exit 1
fi


"$OVPN" client list >"$TMP_DIR/client-list.out"
grep -Eq '^CLIENT ID[[:space:]]+NAME[[:space:]]+STATE$' "$TMP_DIR/client-list.out"
grep -E "^$(short_client_id "$laptop_id")[[:space:]]+laptop[[:space:]]+active$" "$TMP_DIR/client-list.out"
grep -E "^${laptop_id}[[:space:]]+laptop[[:space:]]+active$" <("$OVPN" client list -t)
"$OVPN" client export -i "$(short_client_id "$laptop_id")" >"$TMP_DIR/laptop-by-listed-id.ovpn"
grep -Fqx "# ovpn-client-id: $laptop_id" "$TMP_DIR/laptop-by-listed-id.ovpn"
if "$OVPN" client list --no-trunc --no-trunc >"$TMP_DIR/list-duplicate.out" 2>"$TMP_DIR/list-duplicate.err"; then
  echo 'duplicate --no-trunc unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq -- '--no-trunc may only be specified once' "$TMP_DIR/list-duplicate.err"
test -f "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

"$OVPN" client create source -d >"$TMP_DIR/rename-create.out" 2>"$TMP_DIR/rename-create.err"
rename_id="$(awk -F, '$2 == "source" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
rename_identity_before="$(sha256sum "$OVPN_DATA_DIR/pki/issued/$rename_id.crt" "$OVPN_DATA_DIR/pki/private/$rename_id.key")"
"$OVPN" client rename --id "${rename_id%%-*}" target >"$TMP_DIR/rename.out" 2>"$TMP_DIR/rename.err"
grep -Fqx "$rename_id,target,active" "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx "$rename_id,target," "$OVPN_DATA_DIR/meta/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/clients/active/source.ovpn"
grep -Fqx "# ovpn-client-id: $rename_id" "$OVPN_DATA_DIR/clients/active/target.ovpn"
grep -Fqx '# ovpn-client-name: target' "$OVPN_DATA_DIR/clients/active/target.ovpn"
[ "$rename_identity_before" = "$(sha256sum "$OVPN_DATA_DIR/pki/issued/$rename_id.crt" "$OVPN_DATA_DIR/pki/private/$rename_id.key")" ]
grep -Fq "\"event\":\"client_rename\",\"outcome\":\"applied\",\"client_id\":\"$rename_id\",\"client_name\":\"target\",\"old_name\":\"source\",\"legacy\":false" "$OVPN_DATA_DIR/meta/audit.jsonl"
jq -e -s --arg id "$rename_id" '
  any(.[]; .event == "client_lifecycle" and .operation == "rename" and
    .outcome == "applied" and .client_id == $id and
    .client_name == "target" and .old_name == "source")
' "$OVPN_DATA_DIR/logs/events.jsonl" >/dev/null
[ "$("$OVPN" state show)" = HEALTHY ]

if "$OVPN" client rename target laptop >"$TMP_DIR/rename-conflict.out" 2>"$TMP_DIR/rename-conflict.err"; then
  echo 'rename to a current client name unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'client name already exists: laptop' "$TMP_DIR/rename-conflict.err"
rename_before_failure="$(repair_snapshot)"
rename_events_before_failure="$(jq -s --arg id "$rename_id" \
  '[.[] | select(.event == "client_lifecycle" and .operation == "rename" and .client_id == $id)] | length' \
  "$OVPN_DATA_DIR/logs/events.jsonl")"
if OVPN_CLIENT_RENAME_FAIL_AFTER=registries "$OVPN" client rename target broken >"$TMP_DIR/rename-failure.out" 2>"$TMP_DIR/rename-failure.err"; then
  echo 'injected client rename failure unexpectedly succeeded' >&2
  exit 1
fi
[ "$rename_before_failure" = "$(repair_snapshot)" ] || {
  echo 'failed client rename did not roll back every persisted target' >&2
  exit 1
}
test ! -e "$OVPN_DATA_DIR/clients/active/broken.ovpn"
[ "$(jq -s --arg id "$rename_id" \
  '[.[] | select(.event == "client_lifecycle" and .operation == "rename" and .client_id == $id)] | length' \
  "$OVPN_DATA_DIR/logs/events.jsonl")" = "$rename_events_before_failure" ]
if compgen -G "$OVPN_DATA_DIR/meta/.client-rename.*" >/dev/null; then
  echo 'failed client rename left a staging directory' >&2
  exit 1
fi
"$OVPN" client rename --name target source >"$TMP_DIR/rename-back.out" 2>"$TMP_DIR/rename-back.err"
grep -E "^$(short_client_id "$rename_id")[[:space:]]+source[[:space:]]+active$" <("$OVPN" client list)

"$OVPN" client create phone --dynamic >"$TMP_DIR/phone-create.out" 2>"$TMP_DIR/phone-create.err"
phone_id="$(awk -F, '$2 == "phone" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
grep -Fqx "$phone_id,phone," "$OVPN_DATA_DIR/meta/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/$phone_id"

"$OVPN" client ip set -i "${phone_id%%-*}" -I 10.88.0.20 >"$TMP_DIR/phone-static.out" 2>"$TMP_DIR/phone-static.err"
grep -Fqx "$phone_id,phone,10.88.0.20" "$OVPN_DATA_DIR/meta/client-ip.csv"
grep -Fqx 'ifconfig-push 10.88.0.20 255.255.255.0' "$OVPN_DATA_DIR/ccd/$phone_id"
"$OVPN" client ip set laptop -d >"$TMP_DIR/laptop-dynamic.out" 2>"$TMP_DIR/laptop-dynamic.err"
grep -Fqx "$laptop_id,laptop," "$OVPN_DATA_DIR/meta/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/$laptop_id"
env -u OVPN_EDITOR -u EDITOR \
  OVPN_TEST_EDITOR_LOG="$TMP_DIR/editor.log" \
  PATH="$FAKE_BIN:$PATH" \
  "$OVPN" client ip set laptop phone >"$TMP_DIR/batch-unchanged.out" 2>"$TMP_DIR/batch-unchanged.err"
grep -Eq '^nano:\.client-ip-set\.' "$TMP_DIR/editor.log"
grep -Fqx "$laptop_id,laptop," "$OVPN_DATA_DIR/meta/client-ip.csv"
grep -Fqx "$phone_id,phone,10.88.0.20" "$OVPN_DATA_DIR/meta/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/$laptop_id"
"$OVPN" client ip set -i "$laptop_id" >"$TMP_DIR/laptop-static.out" 2>"$TMP_DIR/laptop-static.err"
grep -Fqx "$laptop_id,laptop,10.88.0.2" "$OVPN_DATA_DIR/meta/client-ip.csv"
grep -Fqx 'ifconfig-push 10.88.0.2 255.255.255.0' "$OVPN_DATA_DIR/ccd/$laptop_id"

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
old_id="$(awk -F, '$2 == "old" && $3 == "revoked" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
"$OVPN" client create online --dynamic >"$TMP_DIR/online-create.out" 2>"$TMP_DIR/online-create.err"
online_id="$(awk -F, '$2 == "online" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
export TEST_ONLINE_ID="$online_id"
"$OVPN" client create known --dynamic >"$TMP_DIR/known-create.out" 2>"$TMP_DIR/known-create.err"
known_id="$(awk -F, '$2 == "known" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
"$OVPN" client create missing --dynamic >"$TMP_DIR/missing-create.out" 2>"$TMP_DIR/missing-create.err"
missing_id="$(awk -F, '$2 == "missing" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
mkdir -p "$OVPN_LEASE_DIR"
printf '10.88.0.201\n' >"$OVPN_LEASE_DIR/$online_id"
printf '10.88.0.202\n' >"$OVPN_LEASE_DIR/$known_id"
printf '10.88.0.203\n' >"$OVPN_LEASE_DIR/unrelated"
"$OVPN" client list -d >"$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row 'CLIENT ID' NAME STATE MODE IP 'IP STATE' CONNECTION)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$laptop_id")" laptop active static 10.88.0.2 configured online)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$phone_id")" phone active static 10.88.0.20 configured offline)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$old_id")" old revoked static 10.88.0.30 retained offline)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$online_id")" online active dynamic 10.88.0.200 connected online)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$known_id")" known active dynamic 10.88.0.202 last-known offline)" "$TMP_DIR/client-list-ip.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$missing_id")" missing active dynamic - unavailable offline)" "$TMP_DIR/client-list-ip.out"
if grep -Fq 'unrelated' "$TMP_DIR/client-list-ip.out"; then
  echo 'client IP list included a lease for an unknown client' >&2
  exit 1
fi
grep -E "^$(short_client_id "$laptop_id")[[:space:]]+laptop[[:space:]]+active$" <("$OVPN" client list)
grep -Fqx "$(format_client_list_row "$(short_client_id "$online_id")" online active dynamic 10.88.0.200 connected online)" <("$OVPN" client list --detail)
grep -Fqx "$(format_client_list_full_row "$online_id" online active dynamic 10.88.0.200 connected online)" <("$OVPN" client list -t -d)
rm -f "$OVPN_MANAGEMENT_SOCKET"
"$OVPN" client list --detail >"$TMP_DIR/client-list-ip-unknown.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$laptop_id")" laptop active static 10.88.0.2 configured unknown)" "$TMP_DIR/client-list-ip-unknown.out"
grep -Fqx "$(format_client_list_row "$(short_client_id "$online_id")" online active dynamic 10.88.0.201 last-known unknown)" "$TMP_DIR/client-list-ip-unknown.out"

"$OVPN" client export -i "${laptop_id%%-*}" >"$TMP_DIR/laptop.ovpn" 2>"$TMP_DIR/export.err"
test ! -s "$TMP_DIR/export.err"
grep -q '^remote vpn.example.test 1194$' "$TMP_DIR/laptop.ovpn"
grep -q "FAKE CLIENT CERT $laptop_id" "$TMP_DIR/laptop.ovpn"
grep -q "FAKE CLIENT KEY $laptop_id" "$TMP_DIR/laptop.ovpn"
grep -Fqx "# ovpn-client-id: $laptop_id" "$TMP_DIR/laptop.ovpn"
grep -Fqx '# ovpn-client-name: laptop' "$TMP_DIR/laptop.ovpn"

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

if "$OVPN" client create "$laptop_id" >"$TMP_DIR/invalid-name.out" 2>"$TMP_DIR/invalid-name.err"; then
  echo 'UUID-shaped display name unexpectedly succeeded' >&2
  exit 1
fi
grep -q 'invalid client name' "$TMP_DIR/invalid-name.err"

if "$OVPN" client create laptop >"$TMP_DIR/duplicate.out" 2>"$TMP_DIR/duplicate.err"; then
  echo 'duplicate add-client unexpectedly succeeded' >&2
  exit 1
fi

grep -q 'already exists' "$TMP_DIR/duplicate.err"

"$OVPN" client revoke --id "${phone_id%%-*}" -r >"$TMP_DIR/phone-revoke.out" 2>"$TMP_DIR/phone-revoke.err"
grep -E "^$(short_client_id "$phone_id")[[:space:]]+phone[[:space:]]+revoked$" <("$OVPN" client list)
grep -Fqx "$phone_id,phone," "$OVPN_DATA_DIR/meta/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/ccd/$phone_id"
test -f "$OVPN_DATA_DIR/clients/revoked/phone.ovpn"
phone_key_before="$(sha256sum "$OVPN_DATA_DIR/pki/private/$phone_id.key")"
"$OVPN" client reissue -n phone >"$TMP_DIR/phone-reissue.out" 2>"$TMP_DIR/phone-reissue.err"
phone_key_after="$(sha256sum "$OVPN_DATA_DIR/pki/private/$phone_id.key")"
[ "$phone_key_before" != "$phone_key_after" ] || {
  echo 'reissue did not generate a new private key' >&2
  exit 1
}
grep -E "^$(short_client_id "$phone_id")[[:space:]]+phone[[:space:]]+active$" <("$OVPN" client list)
grep -q "^$phone_id,phone,10\\.88\\.0\\." "$OVPN_DATA_DIR/meta/client-ip.csv" || {
  echo 'reissue did not auto-allocate a static IP for a client with no IP' >&2
  exit 1
}
test -f "$OVPN_DATA_DIR/clients/active/phone.ovpn"
"$OVPN" client delete -i "${phone_id%%-*}" >"$TMP_DIR/phone-delete.out" 2>"$TMP_DIR/phone-delete.err"
if grep -Fq ',phone,' "$OVPN_DATA_DIR/meta/client-ip.csv"; then
  echo 'deleted client remained in the IP registry' >&2
  exit 1
fi
grep -Fqx "$phone_id,phone,deleted" "$OVPN_DATA_DIR/meta/client-state.csv"
test ! -e "$OVPN_DATA_DIR/pki/private/$phone_id.key"
test ! -e "$OVPN_DATA_DIR/clients/active/phone.ovpn"
if "$OVPN" client export -i "$phone_id" >"$TMP_DIR/deleted-export.out" 2>"$TMP_DIR/deleted-export.err"; then
  echo 'deleted client UUID unexpectedly resolved' >&2
  exit 1
fi
grep -Fq "client ID '$phone_id' does not exist" "$TMP_DIR/deleted-export.err"
"$OVPN" client create phone --dynamic >"$TMP_DIR/reused-name-create.out" 2>"$TMP_DIR/reused-name-create.err"
reused_phone_id="$(awk -F, '$2 == "phone" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
[ "$reused_phone_id" != "$phone_id" ]
grep -Fqx "$phone_id,phone,deleted" "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx "$reused_phone_id,phone,active" "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx "$reused_phone_id,phone," "$OVPN_DATA_DIR/meta/client-ip.csv"
[ "$("$OVPN" state show)" = HEALTHY ]

tablet_index_before="$(sha256sum "$OVPN_DATA_DIR/pki/index.txt")"
if FAKE_EASYRSA_FAIL_BUILD_CLIENT="$tablet_id" "$OVPN" client reissue tablet -d >"$TMP_DIR/tablet-reissue.out" 2>"$TMP_DIR/tablet-reissue.err"; then
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

"$OVPN" client revoke -i "$laptop_id" >"$TMP_DIR/revoke.out" 2>"$TMP_DIR/revoke.err"
grep -Fq "\"event\":\"client_lifecycle\",\"operation\":\"revoke\",\"outcome\":\"applied\",\"client_id\":\"$laptop_id\",\"client_name\":\"laptop\",\"legacy\":false" "$OVPN_DATA_DIR/meta/audit.jsonl"
grep -E "^$(short_client_id "$laptop_id")[[:space:]]+laptop[[:space:]]+revoked$" <("$OVPN" client list)
grep -Fqx "$laptop_id,laptop,10.88.0.2" "$OVPN_DATA_DIR/meta/client-ip.csv"
test -f "$OVPN_DATA_DIR/clients/revoked/laptop.ovpn"
test ! -e "$OVPN_DATA_DIR/clients/active/laptop.ovpn"

"$OVPN" client ip release --name laptop >"$TMP_DIR/release-ip.out" 2>"$TMP_DIR/release-ip.err"
grep -Fqx "$laptop_id,laptop," "$OVPN_DATA_DIR/meta/client-ip.csv"
grep -E "^$(short_client_id "$laptop_id")[[:space:]]+laptop[[:space:]]+revoked$" <("$OVPN" client list)
test ! -e "$OVPN_DATA_DIR/ccd/$laptop_id"
test -f "$OVPN_DATA_DIR/clients/revoked/laptop.ovpn"
test -f "$OVPN_DATA_DIR/pki/private/$laptop_id.key"
grep -Fq release_ip "$OVPN_DATA_DIR/meta/audit.jsonl"
grep -Fq "\"event\":\"client_lifecycle\",\"operation\":\"release_ip\",\"outcome\":\"applied\",\"client_id\":\"$laptop_id\",\"client_name\":\"laptop\",\"legacy\":false" "$OVPN_DATA_DIR/meta/audit.jsonl"
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
"$OVPN" client rename -i "$laptop_id" retired-laptop >"$TMP_DIR/rename-revoked.out" 2>"$TMP_DIR/rename-revoked.err"
grep -E "^$(short_client_id "$laptop_id")[[:space:]]+retired-laptop[[:space:]]+revoked$" <("$OVPN" client list)
grep -Fqx "$laptop_id,retired-laptop,revoked" "$OVPN_DATA_DIR/meta/client-state.csv"
grep -Fqx "$laptop_id,retired-laptop," "$OVPN_DATA_DIR/meta/client-ip.csv"
test ! -e "$OVPN_DATA_DIR/clients/revoked/laptop.ovpn"
grep -Fqx '# ovpn-client-name: retired-laptop' "$OVPN_DATA_DIR/clients/revoked/retired-laptop.ovpn"
[ "$("$OVPN" state show)" = HEALTHY ]

"$OVPN" client create revoke-failure --dynamic >"$TMP_DIR/revoke-failure-create.out" 2>"$TMP_DIR/revoke-failure-create.err"
revoke_failure_id="$(awk -F, '$2 == "revoke-failure" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
revoke_failure_pki_before="$(pki_snapshot)"
mkdir -p "$OVPN_DATA_DIR/.pki-operation.stale/pki/private"
printf 'stale private material\n' >"$OVPN_DATA_DIR/.pki-operation.stale/pki/private/client.key"
if FAKE_EASYRSA_FAIL_REVOKE="$revoke_failure_id" \
  "$OVPN" client revoke revoke-failure >"$TMP_DIR/revoke-failure.out" 2>"$TMP_DIR/revoke-failure.err"; then
  echo 'failed Easy-RSA revoke was reported as successful' >&2
  exit 1
fi
grep -Fq 'failed to revoke client certificate' "$TMP_DIR/revoke-failure.err"
grep -Fqx "$revoke_failure_id,revoke-failure,active" "$OVPN_DATA_DIR/meta/client-state.csv"
[ "$revoke_failure_pki_before" = "$(pki_snapshot)" ]
test ! -e "$OVPN_DATA_DIR/.pki-operation.stale"

if FAKE_EASYRSA_FAIL_GEN_CRL=true \
  "$OVPN" client revoke revoke-failure >"$TMP_DIR/crl-failure.out" 2>"$TMP_DIR/crl-failure.err"; then
  echo 'failed Easy-RSA CRL generation was reported as successful' >&2
  exit 1
fi
grep -Fq 'failed to revoke client certificate' "$TMP_DIR/crl-failure.err"
grep -Fqx "$revoke_failure_id,revoke-failure,active" "$OVPN_DATA_DIR/meta/client-state.csv"
[ "$revoke_failure_pki_before" = "$(pki_snapshot)" ]
[ "$("$OVPN" state show)" = HEALTHY ]

"$OVPN" client create issuance-failure --dynamic >"$TMP_DIR/issuance-failure-create.out" 2>"$TMP_DIR/issuance-failure-create.err"
issuance_failure_id="$(awk -F, '$2 == "issuance-failure" && $3 == "active" { print $1 }' "$OVPN_DATA_DIR/meta/client-state.csv")"
if FAKE_EASYRSA_FAIL_BUILD_CLIENT_AFTER_OUTPUT="$issuance_failure_id" \
  "$OVPN" client reissue issuance-failure >"$TMP_DIR/issuance-failure.out" 2>"$TMP_DIR/issuance-failure.err"; then
  echo 'failed Easy-RSA issuance was reported as successful' >&2
  exit 1
fi
grep -Fq 'client reissue failed' "$TMP_DIR/issuance-failure.err"
grep -Fqx "$issuance_failure_id,issuance-failure,revoked" "$OVPN_DATA_DIR/meta/client-state.csv"
[ "$("$OVPN" state show)" = HEALTHY ]

printf 'client lifecycle smoke passed\n'
