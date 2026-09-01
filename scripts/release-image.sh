#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "usage: $0 [--image-only] vX.Y.Z [output-directory]" >&2
}

image_only=false
if [[ "${1:-}" == "--image-only" ]]; then
  image_only=true
  shift
fi

version="${1:-}"
output_dir="${2:-artifacts}"
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  usage
  echo "error: version must be a stable SemVer tag such as v2.0.0" >&2
  exit 2
fi

for command_name in docker git; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "error: $command_name is required" >&2
    exit 1
  fi
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
cd "$repo_root"

service="appstore"
platform="${TARGET_PLATFORM:-linux/amd64}"
commit="${COMMIT:-$(git rev-parse HEAD)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
image_ref="$service:$version"

echo "Building $image_ref for $platform"
docker build \
  --platform "$platform" \
  --load \
  --build-arg VERSION="$version" \
  --build-arg COMMIT="$commit" \
  --build-arg BUILD_DATE="$build_date" \
  --label "org.opencontainers.image.version=$version" \
  --label "org.opencontainers.image.revision=$commit" \
  --label "org.opencontainers.image.created=$build_date" \
  --tag "$image_ref" \
  .

actual_version="$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$image_ref")"
if [[ "$actual_version" != "$version" ]]; then
  echo "error: image label version is $actual_version, expected $version" >&2
  exit 1
fi

if [[ "$image_only" == true ]]; then
  echo "Built $image_ref"
  exit 0
fi

mkdir -p -- "$output_dir"
archive="$output_dir/$service-$version.tar.gz"
partial="$archive.partial"
cleanup() {
  rm -f -- "$partial"
}
trap cleanup EXIT

echo "Saving $archive"
docker save "$image_ref" | gzip -9n >"$partial"
gzip -t "$partial"
mv -- "$partial" "$archive"
trap - EXIT

"$script_dir/verify-release-archive.sh" "$version" "$archive"
echo "Created $archive"
sha256sum "$archive"
