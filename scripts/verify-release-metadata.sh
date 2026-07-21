#!/bin/sh
set -eu

project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"

set -a
# shellcheck source=../versions.env
. ./versions.env
set +a

go_version=$(awk '$1 == "go" { print $2; exit }' go.mod)
sqlite_version=$(awk '$1 == "github.com/mattn/go-sqlite3" { print $2; exit }' go.mod)
yaml_version=$(awk '$1 == "go.yaml.in/yaml/v3" { print $2; exit }' go.mod)
builder_version=${GO_BUILD_IMAGE#golang:}
builder_version=${builder_version%%-*}
source_version=$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)"/\1/p' internal/buildinfo/info.go)
source_schema=$(sed -n 's/^const DataSchema = \([0-9][0-9]*\)$/\1/p' internal/buildinfo/info.go)

[ -n "$IMAGE_VERSION" ]
[ "$IMAGE_VERSION" = "$GO_RUNTIME_VERSION" ] || {
  echo 'IMAGE_VERSION and GO_RUNTIME_VERSION must match' >&2
  exit 78
}
[ "$IMAGE_VERSION" = "$source_version" ] || {
  echo 'versions.env and buildinfo source version must match' >&2
  exit 78
}
[ "$DATA_SCHEMA" = "$source_schema" ] || {
  echo 'versions.env and buildinfo data schema must match' >&2
  exit 78
}
[ "$go_version" = "$builder_version" ] || {
  echo 'go.mod and GO_BUILD_IMAGE versions must match' >&2
  exit 78
}
[ "$sqlite_version" = v1.14.48 ]
[ "$yaml_version" = v3.0.4 ]

grep -Fq "github.com/mattn/go-sqlite3 $sqlite_version (MIT)" NOTICE
grep -Fq "go.yaml.in/yaml/v3 $yaml_version (MIT and Apache-2.0)" NOTICE

printf 'image_version=%s\n' "$IMAGE_VERSION"
printf 'data_schema=%s\n' "$DATA_SCHEMA"
printf 'go_version=%s\n' "$go_version"
printf 'sqlite=%s %s; license=MIT\n' github.com/mattn/go-sqlite3 "$sqlite_version"
printf 'yaml=%s %s; license=MIT-and-Apache-2.0\n' go.yaml.in/yaml/v3 "$yaml_version"
