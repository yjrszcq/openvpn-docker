#!/usr/bin/env bash
set -euo pipefail

output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
  -o)
    output="$2"
    shift 2
    ;;
  -H | --connect-timeout | --max-time) shift 2 ;;
  -*) shift ;;
  *)
    url="$1"
    shift
    ;;
  esac
done
[ -n "$output" ] || exit 64
case "$url" in
mock://*/releases\?*) source_file="${UPGRADE_FIXTURES:?}/releases.json" ;;
file://*) source_file="${url#file://}" ;;
*) exit 69 ;;
esac
cp "$source_file" "$output"
