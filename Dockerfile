ARG BASE_IMAGE
FROM ${BASE_IMAGE} AS builder

ARG DEBIAN_FRONTEND=noninteractive
ARG OPENVPN_VERSION
ARG OPENVPN_SOURCE_SHA256

RUN test -n "$OPENVPN_VERSION" && test -n "$OPENVPN_SOURCE_SHA256"

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        libcap-ng-dev \
        liblz4-dev \
        liblzo2-dev \
        libnl-genl-3-dev \
        libpam0g-dev \
        libssl-dev \
        pkg-config \
        tar \
    && rm -rf /var/lib/apt/lists/*

COPY scripts/fetch-openvpn-source.sh /usr/local/bin/fetch-openvpn-source

RUN OPENVPN_VERSION="$OPENVPN_VERSION" \
       OPENVPN_SOURCE_SHA256="$OPENVPN_SOURCE_SHA256" \
       /usr/local/bin/fetch-openvpn-source /tmp/source \
    && mkdir -p /work/openvpn \
    && tar -xzf "/tmp/source/openvpn-$OPENVPN_VERSION.tar.gz" \
       --strip-components=1 -C /work/openvpn

WORKDIR /work/openvpn

RUN ./configure --prefix=/usr/local --sbindir=/usr/local/sbin \
    && make -j"$(nproc)" \
    && make DESTDIR=/out install \
    && ldd /out/usr/local/sbin/openvpn \
       | awk '$1 ~ /^\\// { print $1 } $3 ~ /^\\// { print $3 }' \
       | sort -u \
       | xargs -r -I{} cp --parents {} /out

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
        openssl \
        procps \
        tini \
        util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/ /
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
       OVPN_RUNTIME_STRATEGY=source-build \
       OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
       OVPN_VCS_REF="$VCS_REF" \
       OVPN_BUILD_DATE="$BUILD_DATE" \
       /usr/local/bin/generate-build-info /usr/local/share/openvpn-container/build-info.json \
    && rm /usr/local/bin/generate-build-info

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint"]
CMD ["ovpn", "start"]
