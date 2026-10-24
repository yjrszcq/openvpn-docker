ARG BASE=debian:13-slim
FROM ${BASE}
RUN if command -v apk >/dev/null; then \
      apk add --no-cache openrc bash curl tar coreutils kmod python3; \
    elif command -v apt-get >/dev/null; then \
      apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends systemd-sysv udev dbus curl ca-certificates kmod python3 && rm -rf /var/lib/apt/lists/*; \
    elif command -v dnf >/dev/null; then \
      dnf install -y systemd systemd-udev bash tar kmod python3 && dnf clean all; \
    else pacman -Syu --noconfirm systemd-sysvcompat bash curl tar coreutils kmod python3; fi
COPY fixtures/ /fixtures/
RUN if command -v rc-update >/dev/null; then \
      for service in devfs procfs sysfs; do rc-update add "$service" sysinit; done; \
      printf "auto lo\niface lo inet loopback\n" >/etc/network/interfaces && rc-update add networking boot && rc-update add bootmisc boot && rc-update add local default \
      && printf '#!/bin/sh\n/bin/bash /fixtures/vm-guest.sh\n' >/etc/local.d/ovpn-test.start && chmod 755 /etc/local.d/ovpn-test.start; \
    else \
      printf '[Unit]\nAfter=basic.target\n[Service]\nType=oneshot\nExecStart=/bin/bash /fixtures/vm-guest.sh\nTimeoutStartSec=1800\nStandardOutput=journal+console\nStandardError=journal+console\n[Install]\nWantedBy=multi-user.target\n' >/etc/systemd/system/ovpn-test.service \
      && systemctl enable ovpn-test.service; fi
