#!/bin/sh
set -eu

project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"

expected='github.com/yjrszcq/openvpn-docker
github.com/mattn/go-sqlite3 v1.14.48
go.yaml.in/yaml/v3 v3.0.4
gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405'
actual=$(go list -mod=readonly -m all)
if [ "$actual" != "$expected" ]; then
  printf 'unexpected Go module graph:\n%s\n' "$actual" >&2
  exit 1
fi

go mod verify
go list -mod=readonly -m -f '{{if not .Main}}{{.Dir}}{{end}}' all | while IFS= read -r directory; do
  [ -n "$directory" ] || continue
  license=''
  for candidate in "$directory"/LICENSE "$directory"/LICENSE.txt "$directory"/LICENSE.md; do
    if [ -s "$candidate" ]; then
      license=$candidate
      break
    fi
  done
  if [ -z "$license" ]; then
    printf 'module has no top-level license file: %s\n' "$directory" >&2
    exit 1
  fi
  if ! grep -Eq 'Permission is hereby granted|Apache License|Redistribution and use in source and binary forms' "$license"; then
    printf 'module license is outside the approved permissive set: %s\n' "$license" >&2
    exit 1
  fi
done

printf 'Go dependency license gate passed\n'
