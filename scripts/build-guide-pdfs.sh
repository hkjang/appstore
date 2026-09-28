#!/bin/sh
# Bakes docs/USER_GUIDE.pdf and docs/ADMIN_GUIDE.pdf with the shared guide
# renderer, so both covers and both layouts match every other repository that
# follows the cross-repository guide standard.
#
#   GUIDE_TOOL=/path/to/aidev/tools/guide ./scripts/build-guide-pdfs.sh
#
# Links inside the Markdown are relative to docs/. Chromium resolves them
# against the temporary build directory and freezes the result into the PDF, so
# every cross-document and guide link would point at the builder's own disk.
# They are rewritten to the published documentation first, and the result is
# checked: a guide that ships dead file:// links is worse than one that fails
# to build.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DOCS="$ROOT/docs"
GUIDE_TOOL=${GUIDE_TOOL:-/mnt/c/Users/USER/projects/aidev/tools/guide}
VERSION=${VERSION:-v$(sed -n 's/^  "version": "\([^"]*\)".*/\1/p' "$ROOT/web/package.json" | head -n 1)}
PAGES_URL=${PAGES_URL:-https://hkjang.github.io/appstore}
REPOSITORY_URL=${REPOSITORY_URL:-https://github.com/hkjang/appstore}

if [ "$VERSION" = "v" ]; then
    echo "could not determine the release version from web/package.json" >&2
    exit 1
fi
if [ ! -f "$GUIDE_TOOL/md2pdf.mjs" ]; then
    echo "shared guide tool not found at $GUIDE_TOOL (set GUIDE_TOOL)" >&2
    exit 1
fi

BUILD_DIR=$(mktemp -d "$DOCS/.guide-build.XXXXXX")
trap 'rm -rf "$BUILD_DIR"' EXIT HUP INT TERM

# The renderer resolves image paths against the Markdown file, so the build copy
# keeps the same relative layout as docs/.
ln -s "$DOCS/assets" "$BUILD_DIR/assets"

for name in USER_GUIDE ADMIN_GUIDE; do
    case "$name" in
        USER_GUIDE) title="사용자 가이드" ;;
        ADMIN_GUIDE) title="관리자 가이드" ;;
    esac

    markdown="$BUILD_DIR/$name.md"
    cp "$DOCS/$name.md" "$markdown"
    # The sibling guide is read at the tag that shipped it, so the two documents
    # in one PDF pair always describe the same release.
    for target in USER_GUIDE ADMIN_GUIDE; do
        sed -i "s|]($target.md|]($REPOSITORY_URL/blob/$VERSION/docs/$target.md|g" "$markdown"
    done
    sed -i "s|](guides/|]($PAGES_URL/guides/|g" "$markdown"
    if grep -Eq '\]\((\.\./)?[^)":]+\.md([#?][^)]*)?\)' "$markdown"; then
        echo "unresolved local Markdown link in $DOCS/$name.md" >&2
        grep -nE '\]\((\.\./)?[^)":]+\.md([#?][^)]*)?\)' "$markdown" >&2
        exit 1
    fi

    node "$GUIDE_TOOL/md2pdf.mjs" "$markdown" "$DOCS/$name.pdf" \
        --title "$title" --project "AppStore" --version "$VERSION"
    if grep -a -q '/URI (file://' "$DOCS/$name.pdf"; then
        echo "local file URI leaked into $DOCS/$name.pdf" >&2
        exit 1
    fi
    echo "built $DOCS/$name.pdf ($title, $VERSION)"
done
