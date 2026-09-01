#!/usr/bin/env bash
set -Eeuo pipefail

asset_dir="${1:-web/dist}"
if [[ ! -d "$asset_dir" ]]; then
  echo "error: frontend asset directory does not exist: $asset_dir" >&2
  exit 1
fi

blocked="fonts\\.googleapis\\.com|fonts\\.gstatic\\.com|images\\.unsplash\\.com|cdnjs\\.cloudflare\\.com|cdn\\.jsdelivr\\.net|unpkg\\.com|url\\([[:space:]]*['\"]?https?://"
if grep -RInE --include='*.html' --include='*.css' --include='*.js' "$blocked" "$asset_dir"; then
  echo "error: the production frontend contains a blocked runtime CDN asset" >&2
  exit 1
fi

echo "Offline frontend asset check passed"
