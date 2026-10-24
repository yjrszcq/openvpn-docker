#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
architecture="${1:-$(uname -m)}"
case "$architecture" in x86_64 | amd64) architecture=amd64 ;; aarch64 | arm64) architecture=arm64 ;; *)
  echo 'unsupported architecture' >&2
  exit 64
  ;;
esac
output="${2:-$ROOT_DIR/temp/dist}"
mkdir -p "$output"
output="$(cd "$output" && pwd)"
set -a
# shellcheck source=../versions.env
. "$ROOT_DIR/versions.env"
set +a
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
build_date="${SOURCE_DATE_EPOCH:-$(git -C "$ROOT_DIR" show -s --format=%ct HEAD)}"
vcs_ref="$(git -C "$ROOT_DIR" rev-parse HEAD)"
docker buildx build --platform "linux/$architecture" \
  --build-arg "GO_MODULE_PROXY=${GOPROXY:-direct}" \
  --build-arg "IMAGE_VERSION=$IMAGE_VERSION" \
  --build-arg "OPENVPN_VERSION=$OPENVPN_VERSION" \
  --build-arg "OPENVPN_SOURCE_SHA256=$OPENVPN_SOURCE_SHA256" \
  --build-arg "VCS_REF=$vcs_ref" --build-arg "BUILD_DATE=$build_date" \
  --file "$ROOT_DIR/packaging/bundle/Dockerfile" \
  --output "type=local,dest=$stage" "$ROOT_DIR"
archive="openvpn-server-$IMAGE_VERSION-linux-$architecture.tar.gz"
tar --sort=name --mtime="@$build_date" --owner=0 --group=0 --numeric-owner \
  -C "$stage" -cf - openvpn-server | gzip -n >"$output/$archive"
(cd "$output" && sha256sum "$archive" >"$archive.sha256")
printf '%s\n' "$output/$archive"
