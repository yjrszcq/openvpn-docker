ARG BASE_IMAGE
FROM ${BASE_IMAGE}

ARG DEBIAN_FRONTEND=noninteractive
ARG IMAGE_VERSION
ARG OPENVPN_VERSION
ARG OPENVPN_SOURCE_SHA256
ARG EASYRSA_VERSION
ARG OPENVPN_SUPPORTED_RANGE
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown

RUN test -n "$IMAGE_VERSION" \
    && test -n "$OPENVPN_VERSION" \
    && test -n "$OPENVPN_SOURCE_SHA256" \
    && test -n "$EASYRSA_VERSION" \
    && test -n "$OPENVPN_SUPPORTED_RANGE"

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
COPY scripts/generate-build-info.sh /usr/local/bin/generate-build-info

RUN chmod +x /usr/local/bin/ovpn /usr/local/bin/docker-entrypoint \
    && mkdir -p /etc/openvpn /usr/local/share/openvpn-container \
    && IMAGE_VERSION="$IMAGE_VERSION" \
       BASE_IMAGE="$BASE_IMAGE" \
       OPENVPN_VERSION="$OPENVPN_VERSION" \
       OPENVPN_SOURCE_SHA256="$OPENVPN_SOURCE_SHA256" \
       EASYRSA_VERSION="$EASYRSA_VERSION" \
       OPENVPN_SUPPORTED_RANGE="$OPENVPN_SUPPORTED_RANGE" \
       OVPN_RUNTIME_STRATEGY=debian-package-phase1 \
       OVPN_RUNTIME_OPENVPN_VERSION=system \
       OVPN_VCS_REF="$VCS_REF" \
       OVPN_BUILD_DATE="$BUILD_DATE" \
       /usr/local/bin/generate-build-info /usr/local/share/openvpn-container/build-info.json \
    && rm /usr/local/bin/generate-build-info

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint"]
CMD ["ovpn", "start"]
