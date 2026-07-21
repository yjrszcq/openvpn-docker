ARG BASE_IMAGE=debian:trixie-slim
ARG GO_BUILD_IMAGE=golang:1.26.5-trixie

FROM ${GO_BUILD_IMAGE} AS go-builder

ARG GO_RUNTIME_VERSION
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown

ENV CGO_ENABLED=1 \
    GOPROXY=direct

RUN test -n "$GO_RUNTIME_VERSION" \
    && test -n "$VCS_REF" \
    && test -n "$BUILD_DATE"

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

RUN mkdir -p /out/usr/local/lib/openvpn-container/go \
    && go build -buildvcs=false -trimpath \
       -ldflags "-s -w -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.Version=$GO_RUNTIME_VERSION -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.Commit=$VCS_REF -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.BuildDate=$BUILD_DATE" \
       -o /out/usr/local/lib/openvpn-container/go/ovpn ./cmd/ovpn \
    && go build -buildvcs=false -trimpath \
       -ldflags "-s -w -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.Version=$GO_RUNTIME_VERSION -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.Commit=$VCS_REF -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.BuildDate=$BUILD_DATE" \
       -o /out/usr/local/lib/openvpn-container/go/ovpn-broker ./cmd/ovpn-broker \
    && for binary in /out/usr/local/lib/openvpn-container/go/ovpn /out/usr/local/lib/openvpn-container/go/ovpn-broker; do \
         ldd "$binary" >"/tmp/$(basename "$binary").ldd" || exit 1; \
         ! grep -Fq 'not found' "/tmp/$(basename "$binary").ldd" || exit 1; \
         go version -m "$binary" >"/tmp/$(basename "$binary").buildinfo" || exit 1; \
         grep -Fq 'CGO_ENABLED=1' "/tmp/$(basename "$binary").buildinfo" || exit 1; \
       done

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
    && while IFS= read -r library; do \
         resolved="$(readlink -f "$library")" || exit 1; \
         cp --parents "$resolved" /out || exit 1; \
         if [ "$(basename "$library")" != "$(basename "$resolved")" ]; then \
           ln -sf "$(basename "$resolved")" "/out$(dirname "$resolved")/$(basename "$library")" || exit 1; \
         fi; \
       done </tmp/openvpn-lib-paths

FROM ${BASE_IMAGE}
ARG DEBIAN_FRONTEND=noninteractive
ARG OPENVPN_VERSION

RUN test -n "$OPENVPN_VERSION"

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        easy-rsa \
        iproute2 \
        iptables \
        nano \
        openssl \
        tini \
        vim \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/ /
COPY --from=builder /work/openvpn/COPYING /usr/local/share/licenses/openvpn/COPYING
COPY --from=go-builder /out/ /
COPY LICENSE NOTICE /usr/local/share/licenses/openvpn-container/
COPY rootfs/usr/local/share/openvpn-container/templates/ /usr/local/share/openvpn-container/templates/
COPY compatibility/ /usr/local/share/openvpn-container/compatibility/

RUN install -m 0755 /usr/local/lib/openvpn-container/go/ovpn /usr/local/bin/ovpn \
    && install -m 0755 /usr/local/lib/openvpn-container/go/ovpn-broker /usr/local/bin/ovpn-broker \
    && ln -sfn ovpn /usr/local/bin/docker-entrypoint \
    && ln -sfn ovpn /usr/local/bin/ovpn-hook \
    && rm -rf /usr/local/lib/openvpn-container/go \
    && rmdir /usr/local/lib/openvpn-container

RUN openvpn --version >/tmp/openvpn-version \
    && grep -Fq "OpenVPN $OPENVPN_VERSION" /tmp/openvpn-version \
    && ldd /usr/local/sbin/openvpn >/tmp/openvpn-ldd \
    && ! grep -Fq 'not found' /tmp/openvpn-ldd \
    && rm /tmp/openvpn-version /tmp/openvpn-ldd

RUN for binary in /usr/local/bin/ovpn /usr/local/bin/ovpn-broker; do \
      test -x "$binary" || exit 1; \
      ldd "$binary" >"/tmp/$(basename "$binary").ldd" || exit 1; \
      ! grep -Fq 'not found' "/tmp/$(basename "$binary").ldd" || exit 1; \
    done \
    && rm /tmp/ovpn.ldd /tmp/ovpn-broker.ldd

RUN mkdir -p /etc/openvpn /usr/local/share/openvpn-container

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=1 CMD ["/usr/local/bin/ovpn", "runtime", "health"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint"]
