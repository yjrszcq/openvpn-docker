#!/usr/bin/env bash
# Executed inside a disposable container, with only the shipped runtime tools.
set -euo pipefail
unset OVPN_DATA_DIR OVPN_CONFIG_FILE OVPN_RUNTIME_DIR
cp -a /opt/ovpn '/tmp/unconfigured bundle'
mv '/tmp/unconfigured bundle' '/tmp/portable vpn'
root='/tmp/portable vpn'
ln -s "$root/ovpn" /tmp/my-vpn
cd /
/tmp/my-vpn version --short
export PATH="$root/bin:$PATH"
ovpn version --short
if /tmp/my-vpn >/tmp/no-config.log 2>&1; then
  echo 'missing config accepted' >&2
  exit 1
fi
grep -Fq config.example.yaml /tmp/no-config.log
cp "$root/config/config.example.yaml" "$root/config/config.yaml"
sed -i 's/vpn.example.com/127.0.0.1/' "$root/config/config.yaml"
# No arguments must initialize empty state and start the real server.
pid=''
cleanup() { if [ -n "$pid" ]; then
  kill -TERM "$pid" 2>/dev/null || true
  wait "$pid" || true
fi; }
trap cleanup EXIT
iptables -N unrelated_fixture
/tmp/my-vpn >/tmp/portable.log 2>&1 &
pid=$!
for _ in $(seq 1 60); do
  if ovpn runtime health >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! ovpn runtime health; then
  cat /tmp/portable.log
  tail -n 80 "$root/data/logs/openvpn.log" || true
  exit 1
fi
ovpn client create portable-client --ipv4 auto
ovpn client export portable-client --output /tmp/portable-client.ovpn
kill -TERM "$pid"
wait "$pid"
pid=''
iptables -L unrelated_fixture >/dev/null
if iptables-save | grep -E 'OVPN_|ovpn:'; then
  echo 'instance rules remain after stop' >&2
  exit 1
fi
if ip link show tun0 >/dev/null 2>&1; then
  echo 'TUN remains after stop' >&2
  exit 1
fi
# Derived absolute paths are rebuilt through the supported repair operation.
mv "$root" '/tmp/moved vpn'
root='/tmp/moved vpn'
export PATH="$root/bin:$PATH"
ovpn repair apply --yes
ovpn state doctor
if grep -Fq '/tmp/portable vpn' "$root/data/server/server.conf"; then
  echo 'old program path remains' >&2
  exit 1
fi
"$root/ovpn" >/tmp/portable.log 2>&1 &
pid=$!
for _ in $(seq 1 60); do
  if ovpn runtime health >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! ovpn runtime health; then
  cat /tmp/portable.log
  tail -n 80 "$root/data/logs/openvpn.log" || true
  exit 1
fi
kill -TERM "$pid"
wait "$pid"
pid=''
echo 'bundle relocation and signal cleanup passed'
