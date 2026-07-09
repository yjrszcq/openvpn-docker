FROM debian:trixie-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        easy-rsa \
        iproute2 \
        iptables \
        openvpn \
        openssl \
        procps \
        tini \
        util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY rootfs/ /

RUN chmod +x /usr/local/bin/ovpn /usr/local/bin/docker-entrypoint \
    && mkdir -p /etc/openvpn

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint"]
CMD ["ovpn", "start"]
