#!/usr/bin/env bash
set -Eeuo pipefail

version="${1:-}"
archive="${2:-}"
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "usage: $0 vX.Y.Z appstore-vX.Y.Z.tar.gz" >&2
  exit 2
fi

expected_name="appstore-$version.tar.gz"
if [[ -z "$archive" || "$(basename -- "$archive")" != "$expected_name" ]]; then
  echo "error: archive name must be $expected_name" >&2
  exit 2
fi
if [[ ! -s "$archive" ]]; then
  echo "error: archive does not exist or is empty: $archive" >&2
  exit 1
fi

gzip -t "$archive"
manifest="$(gzip -dc -- "$archive" | tar -xOf - manifest.json)"
expected_ref="appstore:$version"
if [[ "$manifest" != *"\"$expected_ref\""* ]]; then
  echo "error: Docker archive does not contain $expected_ref" >&2
  exit 1
fi

if command -v docker >/dev/null 2>&1; then
  gzip -dc -- "$archive" | docker load >/dev/null
  actual_version="$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$expected_ref")"
  if [[ "$actual_version" != "$version" ]]; then
    echo "error: loaded image version label is $actual_version, expected $version" >&2
    exit 1
  fi
  actual_user="$(docker image inspect --format '{{ .Config.User }}' "$expected_ref")"
  if [[ -z "$actual_user" || "$actual_user" == "0" || "$actual_user" == "root" ]]; then
    echo "error: loaded image is configured to run as root" >&2
    exit 1
  fi
fi

echo "Verified $archive contains non-root $expected_ref"
