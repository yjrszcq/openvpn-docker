#!/usr/bin/env bash
# Runs after systemd/OpenRC boots as PID 1 inside an isolated VM.
set -euo pipefail
exec > >(tee /dev/console) 2>&1
finish() {
  code=$?
  trap - EXIT
  if [ "$code" = 0 ]; then echo OVPN_VM_TEST_PASS; else
    echo "OVPN_VM_TEST_FAIL=$code"
    if [ -d /run/systemd/system ]; then journalctl -b --no-pager -u ovpn -u dbus -n 80 || true; fi
  fi
  sync
  if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then systemctl poweroff --no-block; else poweroff; fi
}
trap finish EXIT
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
cat /etc/os-release
ls -ld /dev /dev/null /opt
modprobe tun
mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200
if [ -f /fixtures/rebooted ]; then
  for _ in $(seq 1 60); do
    if /usr/local/bin/ovpn runtime health; then break; fi
    sleep 1
  done
  /usr/local/bin/ovpn runtime health
  test "$(/usr/local/bin/ovpn version --short)" = "$(cat /fixtures/new-version)"
  exit 0
fi
# Serve actual release assets inside the guest; no GitHub write or mocked service.
python3 -m http.server 18080 --bind 127.0.0.1 --directory /fixtures/releases >/run/fixture-http.log 2>&1 &
for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18080/SHA256SUMS >/dev/null; then break; fi
  sleep 1
done
export OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080
export OVPN_VERSION
for scenario in corrupt missing unsafe wrong-arch wrong-schema unlisted; do
  if OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_DIR=/opt/ovpn-negative OVPN_COMMAND_LINK=/usr/local/bin/ovpn-negative OVPN_DOWNLOAD_BASE_URL="http://127.0.0.1:18080/$scenario" bash /fixtures/install.sh >/run/negative.log 2>&1; then
    echo "invalid asset accepted: $scenario"
    exit 1
  fi
  test ! -e /opt/ovpn-negative
  test ! -e /usr/local/bin/ovpn-negative
  test ! -e /tmp/escaped
  echo "asset rejection passed: $scenario"
done
OVPN_VERSION="$(cat /fixtures/old-version)"
bash /fixtures/install.sh
test -L /usr/local/bin/ovpn
bash /fixtures/install.sh
cp /opt/ovpn/config/config.example.yaml /opt/ovpn/config/config.yaml
sed -i 's/vpn.example.com/127.0.0.1/' /opt/ovpn/config/config.yaml
service_action() {
  if [ -d /run/systemd/system ]; then systemctl "$1" ovpn; else rc-service ovpn "$1"; fi
}
if service_action status; then
  echo 'first install unexpectedly started service'
  exit 1
fi
if [ -d /run/systemd/system ]; then
  if systemctl is-enabled ovpn; then
    echo 'first install unexpectedly enabled service'
    exit 1
  fi
else
  test ! -e /etc/runlevels/default/ovpn
fi
# Existing command conflicts must leave both the command and target intact.
printf 'existing command\n' >/usr/local/bin/ovpn-conflict
if OVPN_INSTALL_DIR=/opt/ovpn-conflict OVPN_COMMAND_LINK=/usr/local/bin/ovpn-conflict bash /fixtures/install.sh >/run/conflict.log 2>&1; then exit 1; fi
grep -Fq 'existing command' /usr/local/bin/ovpn-conflict
test ! -e /opt/ovpn-conflict
if [ -d /run/systemd/system ]; then systemctl enable ovpn; else rc-update add ovpn default; fi
service_action start
for _ in $(seq 1 60); do
  if ovpn runtime health; then break; fi
  sleep 1
done
ovpn runtime health
ovpn client create vm-client --ipv4 auto --output /opt/ovpn/config/vm-client.ovpn
certificate="$(sha256sum /opt/ovpn/data/pki/ca.crt)"
chmod 640 /opt/ovpn/config/vm-client.ovpn
profile="$(sha256sum /opt/ovpn/config/vm-client.ovpn)"
service_action stop
OVPN_VERSION="$(cat /fixtures/middle-version)" OVPN_INSTALL_ACTION=update bash /fixtures/install.sh
if service_action status; then echo 'stopped upgrade unexpectedly started service'; exit 1; fi
test "$(ovpn version --short)" = "$(cat /fixtures/middle-version)"
service_action start
for _ in $(seq 1 60); do if ovpn runtime health; then break; fi; sleep 1; done
# The lock must reject another live updater without stopping the service.
exec 9<>/opt/ovpn.install-lock
/opt/ovpn/bin/flock -n 9
if OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update bash /fixtures/install.sh >/run/concurrent.log 2>&1; then exit 1; fi
grep -Fq 'another installation/update is active' /run/concurrent.log
exec 9>&-
ovpn runtime health
OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update bash /fixtures/install.sh
ovpn runtime health
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate"
test "$(sha256sum /opt/ovpn/config/vm-client.ovpn)" = "$profile"
test "$(stat -c %a /opt/ovpn/config/vm-client.ovpn)" = 640
test "$(ovpn version --short)" = "$(cat /fixtures/new-version)"
service_action stop
ovpn >/run/unmanaged.log 2>&1 &
unmanaged=$!
for _ in $(seq 1 60); do if ovpn runtime health; then break; fi; sleep 1; done
ovpn runtime health
if OVPN_VERSION="$(cat /fixtures/bad-version)" OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080/bad OVPN_INSTALL_ACTION=update bash /fixtures/install.sh >/run/unmanaged-update.log 2>&1; then exit 1; fi
grep -Fq 'unmanaged instance detected' /run/unmanaged-update.log
ovpn runtime health
kill -TERM "$unmanaged"
wait "$unmanaged"
service_action start
# A deliberately broken, checksum-valid candidate must trigger real rollback.
if OVPN_VERSION="$(cat /fixtures/bad-version)" OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080/bad OVPN_INSTALL_ACTION=update bash /fixtures/install.sh; then
  echo 'broken update unexpectedly succeeded' >&2
  exit 1
fi
ovpn runtime health
test "$(ovpn version --short)" = "$(cat /fixtures/new-version)"
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate"
# Kill a real updater after replacement, before its failing health check ends.
OVPN_VERSION="$(cat /fixtures/bad-version)" OVPN_DOWNLOAD_BASE_URL=http://127.0.0.1:18080/bad OVPN_INSTALL_ACTION=update bash /fixtures/install.sh >/run/interrupted.log 2>&1 &
updater=$!
for _ in $(seq 1 30); do
  if [ -f /opt/ovpn/.update-backup/ready ] && [ "$(ovpn version --short)" = "$(cat /fixtures/bad-version)" ]; then break; fi
  sleep 1
done
test -f /opt/ovpn/.update-backup/ready
kill -KILL "$updater"
wait "$updater" || true
OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update bash /fixtures/install.sh
ovpn runtime health
test "$(sha256sum /opt/ovpn/data/pki/ca.crt)" = "$certificate"
test ! -e /opt/ovpn/.update-backup
service_action stop
OVPN_VERSION="$(cat /fixtures/new-version)" OVPN_INSTALL_ACTION=update bash /fixtures/install.sh
if service_action status; then
  echo 'stopped service unexpectedly started' >&2
  exit 1
fi
# A second boot proves persistence and enabled-service startup.
if [ ! -f /fixtures/rebooted ]; then
  touch /fixtures/rebooted
  service_action start
  echo OVPN_VM_REBOOT
  sync
  trap - EXIT
  reboot
fi
