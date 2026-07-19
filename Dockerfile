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
    && make DESTDIR=/out install

RUN ldd /out/usr/local/sbin/openvpn >/tmp/openvpn-libs \
    && awk '$1 ~ /^\// { print $1 } $3 ~ /^\// { print $3 }' /tmp/openvpn-libs >/tmp/openvpn-lib-paths \
    && sort -u /tmp/openvpn-lib-paths -o /tmp/openvpn-lib-paths \
    && test -s /tmp/openvpn-lib-paths \
    && while IFS= read -r library; do resolved="$(readlink -f "$library")"; cp --parents "$resolved" /out; if [ "$(basename "$library")" != "$(basename "$resolved")" ]; then ln -sf "$(basename "$resolved")" "/out$(dirname "$resolved")/$(basename "$library")"; fi; done </tmp/openvpn-lib-paths

FROM ${BASE_IMAGE}
ARG BASE_IMAGE
ARG DEBIAN_FRONTEND=noninteractive
ARG IMAGE_VERSION
ARG MANAGEMENT_VERSION
ARG PLATFORM_API
ARG DATA_SCHEMA
ARG OPENVPN_VERSION
ARG OPENVPN_SOURCE_SHA256
ARG EASYRSA_VERSION
ARG OPENVPN_CANDIDATE_RANGE
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown

RUN test -n "$IMAGE_VERSION" \
    && test -n "$MANAGEMENT_VERSION" \
    && test -n "$PLATFORM_API" \
    && test -n "$DATA_SCHEMA" \
    && test -n "$OPENVPN_VERSION" \
    && test -n "$OPENVPN_SOURCE_SHA256" \
    && test -n "$EASYRSA_VERSION" \
    && test -n "$OPENVPN_CANDIDATE_RANGE"

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        easy-rsa \
        iproute2 \
        iptables \
        jq \
        nano \
        openssl \
        procps \
        python3-minimal \
        tini \
        socat \
        util-linux \
        vim \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/ /
COPY --from=builder /work/openvpn/COPYING /usr/local/share/licenses/openvpn/COPYING
COPY LICENSE NOTICE /usr/local/share/licenses/openvpn-container/
COPY rootfs/ /
COPY compatibility/ /usr/local/share/openvpn-container/compatibility/
COPY scripts/generate-build-info.sh /usr/local/bin/generate-build-info

RUN openvpn --version >/tmp/openvpn-version \
    && grep -Fq "OpenVPN $OPENVPN_VERSION" /tmp/openvpn-version \
    && ldd /usr/local/sbin/openvpn >/tmp/openvpn-ldd \
    && ! grep -Fq 'not found' /tmp/openvpn-ldd \
    && rm /tmp/openvpn-version /tmp/openvpn-ldd

RUN chmod +x /usr/local/bin/ovpn /usr/local/bin/ovpn-hook /usr/local/bin/docker-entrypoint \
       /usr/local/lib/openvpn-container/cli.sh \
    && mkdir -p /etc/openvpn /usr/local/share/openvpn-container \
    && IMAGE_VERSION="$IMAGE_VERSION" \
       MANAGEMENT_VERSION="$MANAGEMENT_VERSION" \
       PLATFORM_API="$PLATFORM_API" \
       DATA_SCHEMA="$DATA_SCHEMA" \
       BASE_IMAGE="$BASE_IMAGE" \
       OPENVPN_VERSION="$OPENVPN_VERSION" \
       OPENVPN_SOURCE_SHA256="$OPENVPN_SOURCE_SHA256" \
       EASYRSA_VERSION="$EASYRSA_VERSION" \
       OPENVPN_CANDIDATE_RANGE="$OPENVPN_CANDIDATE_RANGE" \
       OVPN_RUNTIME_STRATEGY=source-build \
       OVPN_RUNTIME_OPENVPN_VERSION="$OPENVPN_VERSION" \
       OVPN_VCS_REF="$VCS_REF" \
       OVPN_BUILD_DATE="$BUILD_DATE" \
       /usr/local/bin/generate-build-info /usr/local/share/openvpn-container/build-info.json \
    && rm /usr/local/bin/generate-build-info

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=1 CMD ["/usr/local/bin/ovpn", "runtime", "health"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint"]
CMD ["ovpn", "start"]
