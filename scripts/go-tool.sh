#!/bin/sh
set -eu

project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
go_image=${GO_IMAGE:-golang:1.27.3-trixie}

case "$(uname -m)" in
x86_64) native_platform=linux/amd64 ;;
aarch64 | arm64) native_platform=linux/arm64 ;;
*)
  echo "unsupported native Go Docker platform: $(uname -m)" >&2
  exit 69
  ;;
esac
go_platform=${GO_PLATFORM:-$native_platform}

# Prefer the versioned SDK over the system default Go installation.
# GOTOOLCHAIN=local prevents an automatic toolchain download from masking a
# mismatched executable during the version check or the requested command.
for go_binary in "${HOME}/sdk/go1.27.3/bin/go" go; do
  if command -v "$go_binary" >/dev/null 2>&1; then
    local_go_version=$(GOTOOLCHAIN=local "$go_binary" env GOVERSION 2>/dev/null || true)
    if [ "$local_go_version" = go1.27.3 ]; then
      export GOTOOLCHAIN=local
      exec "$go_binary" "$@"
    fi
  fi
done

exec docker run --rm \
  --platform "$go_platform" \
  --user "$(id -u):$(id -g)" \
  --env CGO_ENABLED="${CGO_ENABLED:-1}" \
  --env GOCACHE=/tmp/go-build \
  --env GOMODCACHE=/tmp/go-mod \
  --volume "$project_root:/src" \
  --workdir /src \
  "$go_image" go "$@"
