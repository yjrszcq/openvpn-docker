#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
go_image=${GO_IMAGE:-golang:1.26.5-trixie}

case "$(uname -m)" in
    x86_64) native_platform=linux/amd64 ;;
    aarch64 | arm64) native_platform=linux/arm64 ;;
    *)
        echo "unsupported native Go Docker platform: $(uname -m)" >&2
        exit 69
        ;;
esac
go_platform=${GO_PLATFORM:-$native_platform}

local_go_version=''
if command -v go >/dev/null 2>&1; then
    local_go_version=$(go env GOVERSION 2>/dev/null || true)
fi
if [ "$local_go_version" = go1.26.5 ]; then
    exec go "$@"
fi

exec docker run --rm \
    --platform "$go_platform" \
    --user "$(id -u):$(id -g)" \
    --env CGO_ENABLED="${CGO_ENABLED:-1}" \
    --env GOCACHE=/tmp/go-build \
    --env GOMODCACHE=/tmp/go-mod \
    --volume "$project_root:/src" \
    --workdir /src \
    "$go_image" go "$@"
