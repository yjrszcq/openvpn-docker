#!/usr/bin/env bash
set -euo pipefail
installer="$(curl --fail --silent --show-error --location --retry 3 'https://raw.githubusercontent.com/yjrszcq/openvpn-docker/main/scripts/install.sh')"
export OVPN_INSTALL_ACTION=update
exec bash -c "$installer"
