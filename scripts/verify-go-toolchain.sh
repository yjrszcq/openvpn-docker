#!/bin/sh
set -eu

project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
go_image=${GO_IMAGE:-golang:1.26.5-trixie}

for platform in linux/amd64 linux/arm64; do
  docker run --rm \
    --platform "$platform" \
    --env CGO_ENABLED=1 \
    --env GOCACHE=/tmp/go-build \
    --env GOMODCACHE=/tmp/go-mod \
    --volume "$project_root:/src:ro" \
    --workdir /src \
    "$go_image" sh -eu -c '
            go test ./...
            go test -c -o /tmp/sqlite-toolchain.test ./internal/platform/toolchain
            ldd /tmp/sqlite-toolchain.test > /tmp/ldd.out
            ! grep -Fq "not found" /tmp/ldd.out
            grep -Fq "libc.so" /tmp/ldd.out
            cat /tmp/ldd.out
        '
done
