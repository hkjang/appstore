#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
docs="$repo_root/docs"

required=(
  index.html 404.html robots.txt sitemap.xml llms.txt manifest.webmanifest
  assets/site.css assets/site.js assets/screenshots/manifest.json
  guides/user/index.html guides/admin/index.html guides/offline/index.html
  guides/upgrade/index.html guides/backup/index.html guides/release/index.html
)
for relative in "${required[@]}"; do
  if [[ ! -s "$docs/$relative" ]]; then
    echo "error: missing docs/$relative" >&2
    exit 1
  fi
done

for marker in 'rel="canonical"' 'property="og:title"' 'application/ld+json' '<h1'; do
  if ! grep -Fq "$marker" "$docs/index.html"; then
    echo "error: docs/index.html is missing $marker" >&2
    exit 1
  fi
done

if grep -RInE --include='*.html' --include='*.css' \
  "(<script[^>]+src|<img[^>]+src|<link[^>]+rel=\"stylesheet\"[^>]+href)=\"https?://|url\\(['\"]?https?://" \
  "$docs"; then
  echo "error: GitHub Pages content references a remote runtime asset" >&2
  exit 1
fi

node - "$docs" <<'NODE'
const fs = require("node:fs");
const path = require("node:path");
const docs = process.argv[2];
const manifestPath = path.join(docs, "assets/screenshots/manifest.json");
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
if (manifest.schemaVersion !== 2 || !/^v\d+\.\d+\.\d+$/.test(manifest.generatedForVersion ?? "")) {
  throw new Error("screenshot manifest must identify the released capture schema and version");
}
if (!Array.isArray(manifest.captures) || manifest.captures.length < 76) {
  throw new Error("screenshot manifest must contain desktop and mobile captures for every route");
}
const captureIds = new Set();
const captureOutputs = new Set();
for (const capture of manifest.captures) {
  if (!capture.route || !capture.routePattern || !capture.viewport || !capture.output || !capture.alt) {
    throw new Error(`invalid screenshot manifest entry: ${JSON.stringify(capture)}`);
  }
  if (capture.status !== "captured" || capture.version !== manifest.generatedForVersion) {
    throw new Error(`capture is not finalized for ${manifest.generatedForVersion}: ${capture.id}`);
  }
  if (!/^[a-f0-9]{64}$/.test(capture.sha256)) {
    throw new Error(`capture checksum is invalid: ${capture.id}`);
  }
  if (captureIds.has(capture.id) || captureOutputs.has(capture.output)) {
    throw new Error(`duplicate capture id or output: ${capture.id}`);
  }
  captureIds.add(capture.id);
  captureOutputs.add(capture.output);
  if (!capture.fullPage || !Number.isInteger(capture.width) || !Number.isInteger(capture.height)) {
    throw new Error(`capture dimensions or full-page flag are invalid: ${capture.id}`);
  }
  const output = path.join(docs, capture.output);
  if (!fs.existsSync(output) || fs.statSync(output).size < 1024) {
    throw new Error(`capture output is missing or empty: ${capture.output}`);
  }
  const bytes = fs.readFileSync(output);
  if (bytes.toString("ascii", 0, 4) !== "RIFF" || bytes.toString("ascii", 8, 12) !== "WEBP") {
    throw new Error(`capture is not a WebP file: ${capture.id}`);
  }
  const actualHash = require("node:crypto")
    .createHash("sha256")
    .update(bytes)
    .digest("hex");
  if (actualHash !== capture.sha256) {
    throw new Error(`capture checksum mismatch: ${capture.id}`);
  }
}

const router = fs.readFileSync(path.join(docs, "../web/src/app/router.tsx"), "utf8");
const routePatterns = [
  "/",
  ...[...router.matchAll(/\bpath="([^"]+)"/g)].map((match) =>
    match[1] === "*"
      ? "*"
      : match[1].startsWith("/")
        ? match[1]
        : `/${match[1]}`,
  ),
];
for (const routePattern of new Set(routePatterns)) {
  for (const viewport of ["desktop", "mobile"]) {
    if (!manifest.captures.some((capture) =>
      capture.routePattern === routePattern && capture.viewport === viewport
    )) {
      throw new Error(`router capture missing: ${routePattern} (${viewport})`);
    }
  }
}
for (const viewport of ["desktop", "mobile"]) {
  if (!manifest.captures.some((capture) =>
    capture.route === "/apps?mcp=true" && capture.viewport === viewport
  )) {
    throw new Error(`MCP Apps canonical capture missing: ${viewport}`);
  }
}
JSON.parse(fs.readFileSync(path.join(docs, "manifest.webmanifest"), "utf8"));

const htmlFiles = [];
const walk = (directory) => {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) walk(absolute);
    else if (entry.name.endsWith(".html")) htmlFiles.push(absolute);
  }
};
walk(docs);
for (const htmlFile of htmlFiles) {
  const html = fs.readFileSync(htmlFile, "utf8");
  for (const match of html.matchAll(/<script\s+type="application\/ld\+json">([\s\S]*?)<\/script>/g)) {
    JSON.parse(match[1]);
  }
  for (const match of html.matchAll(/(?:href|src)="([^"]+)"/g)) {
    const reference = match[1];
    if (/^(?:https?:|mailto:|tel:|#)/.test(reference)) continue;
    const clean = decodeURIComponent(reference.split(/[?#]/, 1)[0]);
    let target;
    if (clean.startsWith("/appstore/")) target = path.join(docs, clean.slice("/appstore/".length));
    else target = path.resolve(path.dirname(htmlFile), clean);
    if (clean.endsWith("/")) target = path.join(target, "index.html");
    if (!fs.existsSync(target)) throw new Error(`broken local reference in ${htmlFile}: ${reference}`);
  }
}
NODE

echo "GitHub Pages documentation check passed"
