#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
source_dir="$repo_root/web/dist"
target_dir="$repo_root/internal/webui/dist"

if [[ ! -f "$source_dir/index.html" ]]; then
  echo "error: web/dist/index.html is missing; run npm build first" >&2
  exit 1
fi

mkdir -p -- "$target_dir"
find "$target_dir" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
cp -a "$source_dir/." "$target_dir/"
echo "Embedded React bundle into internal/webui/dist"
