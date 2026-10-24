ARG GO_BUNDLE_IMAGE=golang:1.27.3-alpine3.24
FROM ${GO_BUNDLE_IMAGE} AS builder
ARG OPENVPN_VERSION
ARG OPENVPN_SOURCE_SHA256
ENV GOTOOLCHAIN=local CGO_ENABLED=1 GOPROXY=direct
RUN apk add --no-cache git build-base linux-headers openssl-dev lzo-dev lz4-dev libcap-ng-dev libnl3-dev curl bash easy-rsa openssl iproute2 iptables iptables-openrc busybox-static pax-utils python3 py3-elftools nano ca-certificates spdx-licenses-text
COPY scripts/fetch-openvpn-source.sh /usr/local/bin/fetch-openvpn-source
RUN OPENVPN_VERSION="$OPENVPN_VERSION" OPENVPN_SOURCE_SHA256="$OPENVPN_SOURCE_SHA256" fetch-openvpn-source /tmp/source \
 && mkdir /tmp/openvpn \
 && tar -xzf "/tmp/source/openvpn-$OPENVPN_VERSION.tar.gz" --strip-components=1 -C /tmp/openvpn \
 && cd /tmp/openvpn && ./configure --prefix=/usr/local --disable-debug --disable-plugin-auth-pam && make -j"$(nproc)" && make install \
 && mkdir -p /bundle/runtime/bin && cp /usr/local/sbin/openvpn /bundle/runtime/bin/
WORKDIR /src
COPY go.mod go.sum ./
ARG GO_MODULE_PROXY=direct
RUN --mount=type=cache,target=/go/pkg/mod GOPROXY="$GO_MODULE_PROXY" go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG IMAGE_VERSION
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build test "$(go env GOVERSION)" = go1.27.3 \
 && mkdir -p /bundle/runtime/bin \
 && for name in ovpn ovpn-broker ovpn-api; do \
      output="$name"; [ "$name" != ovpn ] || output=ovpn-core; \
      go build -buildvcs=false -trimpath -ldflags "-s -w -linkmode external -extldflags -static -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.Version=$IMAGE_VERSION -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.Commit=$VCS_REF -X github.com/yjrszcq/openvpn-docker/internal/buildinfo.BuildDate=$BUILD_DATE" -o "/bundle/runtime/bin/$output" "./cmd/$name" || exit 1; \
    done \
 && CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '-s -w' -o /bundle/ovpn ./cmd/ovpn-launcher
RUN apk add --no-cache flock
COPY packaging/bundle/assemble.py /tmp/assemble.py
COPY templates/ /bundle/runtime/share/templates/
COPY compatibility/contract.json /bundle/runtime/share/compatibility/contract.json
COPY config.example.yaml /bundle/config/config.example.yaml
COPY LICENSE NOTICE /bundle/runtime/licenses/project/
RUN --mount=type=cache,target=/go/pkg/mod IMAGE_VERSION="$IMAGE_VERSION" OPENVPN_VERSION="$OPENVPN_VERSION" VCS_REF="$VCS_REF" BUILD_DATE="$BUILD_DATE" python3 /tmp/assemble.py
FROM scratch AS export
COPY --from=builder /bundle/ /openvpn-server/
