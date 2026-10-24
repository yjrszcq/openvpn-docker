#!/usr/bin/env bash
# Run against the already built bundle container; all failures must be explicit.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
image="${1:?bundle test image required}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
docker run --rm --cap-add NET_ADMIN --entrypoint sh "$image" -ec '
 mkdir -p /etc/ovpn-conf
 cp /opt/ovpn/config/config.example.yaml /etc/ovpn-conf/config.yaml
 # Exercise the userspace TUN path even when the host has an available DCO module.
 printf "\ndisable-dco\n" >>/opt/ovpn/runtime/share/templates/openvpn-2.7/server.conf.tpl
 ovpn server init
 if timeout 20s ovpn >/tmp/no-tun.log 2>&1; then echo "missing TUN accepted"; exit 1; fi
 cat /tmp/no-tun.log
 grep -Ei "TUN|/dev/net/tun" /tmp/no-tun.log
 if iptables-save | grep -E "OVPN_|ovpn:"; then echo "failed start left instance rules"; exit 1; fi
 ovpn state doctor
'
if docker run --rm --user 65534:65534 -e OVPN_CONFIG_FILE=/opt/ovpn/config/config.example.yaml --entrypoint ovpn "$image" server init >"$work/permission.log" 2>&1; then
  echo 'unprivileged initialization unexpectedly succeeded' >&2
  exit 1
fi
if docker run --rm --user 65534:65534 -v "$ROOT_DIR/scripts/install.sh:/install.sh:ro" --entrypoint /opt/ovpn/bin/bash "$image" /install.sh >"$work/install.log" 2>&1; then
  echo 'unprivileged installation unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'installation requires root' "$work/install.log"
echo 'missing TUN and insufficient permissions rejected; startup rules cleaned'
