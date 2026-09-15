#!/usr/bin/env bash
# Copy the user and administrator guides that aidev collects for every
# service into guides/<slug>/, where the server embeds them and attaches
# them to the catalog entry of the same slug on every start.
#
# Usage: scripts/sync-guides.sh [source-directory]
#   source-directory defaults to ../aidev/docs/guides
#
# One directory per service becomes one directory per slug. The slug is
# derived the same way the seed derives it from a repository name: letters
# and digits lowered, every other run of characters collapsed to one dash.
# A PDF is preferred; the Markdown source is copied only when no PDF exists
# yet. Directories that no longer have a source are removed so a renamed
# service does not keep attaching its old guides.
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
source_dir="${1:-$repo_root/../aidev/docs/guides}"
target_dir="$repo_root/guides"

if [[ ! -d "$source_dir" ]]; then
  echo "error: guide source directory not found: $source_dir" >&2
  exit 1
fi

slugify() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//'
}

mkdir -p "$target_dir"
declare -A wanted=()
copied=0
for project in "$source_dir"/*/; do
  name="$(basename "$project")"
  slug="$(slugify "$name")"
  [[ -n "$slug" ]] || continue
  files=()
  for kind in USER_GUIDE ADMIN_GUIDE; do
    if [[ -s "$project/$kind.pdf" ]]; then
      files+=("$kind.pdf")
    elif [[ -s "$project/$kind.md" ]]; then
      files+=("$kind.md")
    fi
  done
  [[ ${#files[@]} -gt 0 ]] || continue
  wanted["$slug"]=1
  mkdir -p "$target_dir/$slug"
  # Remove whatever the slug carried before so a PDF replacing a Markdown
  # source does not leave both behind.
  find "$target_dir/$slug" -mindepth 1 -delete
  for file in "${files[@]}"; do
    cp -- "$project/$file" "$target_dir/$slug/$file"
    copied=$((copied + 1))
  done
done

removed=0
for existing in "$target_dir"/*/; do
  [[ -d "$existing" ]] || continue
  slug="$(basename "$existing")"
  if [[ -z "${wanted[$slug]:-}" ]]; then
    rm -rf -- "$existing"
    removed=$((removed + 1))
  fi
done

echo "guides: ${#wanted[@]} apps, $copied files copied, $removed stale directories removed -> $target_dir"
