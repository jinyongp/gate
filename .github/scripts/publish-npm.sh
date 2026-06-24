#!/usr/bin/env bash
set -euo pipefail

version_tag="${1:?Usage: publish-npm.sh vX.Y.Z [artifact-dir]}"
artifact_dir="${2:-.}"

if [[ ! "$version_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "npm version tag must be vX.Y.Z: ${version_tag}" >&2
  exit 1
fi

if [ -z "${NODE_AUTH_TOKEN:-}" ]; then
  echo "NODE_AUTH_TOKEN is required for npm publish." >&2
  exit 1
fi

node scripts/node/prepare-publish-packages.mjs "$version_tag"
pnpm node:build
pnpm node:stage:binaries "$artifact_dir"

publish_package() {
  local package_dir="$1"
  local spec

  spec="$(node -e 'const fs = require("fs"); const path = require("path"); const manifest = JSON.parse(fs.readFileSync(path.join(process.argv[1], "package.json"), "utf8")); console.log(`${manifest.name}@${manifest.version}`);' "$package_dir")"
  if npm view "$spec" version >/dev/null 2>&1; then
    echo "npm package already exists; skipping ${spec}"
    return
  fi

  npm publish "$package_dir" --access public --provenance
}

publish_package packages/binaries/darwin-arm64
publish_package packages/binaries/darwin-x64
publish_package packages/binaries/linux-arm64
publish_package packages/binaries/linux-x64
publish_package packages/node
