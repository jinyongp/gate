#!/usr/bin/env bash
set -euo pipefail

version_tag="${1:?Usage: publish-npm.sh vX.Y.Z [artifact-dir]}"
artifact_dir="${2:-.}"
summary_file="${NPM_PUBLISH_SUMMARY:-npm-publish-summary.tsv}"
npmjs_registry_config="--@jinyongp:registry=https://registry.npmjs.org"
publish_root="$(mktemp -d)"

cleanup() {
  rm -rf "$publish_root"
}
trap cleanup EXIT

if [[ ! "$version_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "npm version tag must be vX.Y.Z: ${version_tag}" >&2
  exit 1
fi

pnpm node:build

mkdir -p "$publish_root/packages/binaries" "$publish_root/scripts/node"
cp -R packages/node "$publish_root/packages/node"
cp -R packages/binaries/darwin-arm64 "$publish_root/packages/binaries/darwin-arm64"
cp -R packages/binaries/darwin-x64 "$publish_root/packages/binaries/darwin-x64"
cp -R packages/binaries/linux-arm64 "$publish_root/packages/binaries/linux-arm64"
cp -R packages/binaries/linux-x64 "$publish_root/packages/binaries/linux-x64"
cp scripts/node/verify-package-binary.mjs "$publish_root/scripts/node/verify-package-binary.mjs"

node scripts/node/prepare-publish-packages.mjs "$version_tag" "$publish_root"
node scripts/node/stage-binary-packages.mjs "$artifact_dir" "$publish_root"

: > "$summary_file"

publish_package() {
  local package_dir="$1"
  local spec

  spec="$(node -e 'const fs = require("fs"); const path = require("path"); const manifest = JSON.parse(fs.readFileSync(path.join(process.argv[1], "package.json"), "utf8")); console.log(`${manifest.name}@${manifest.version}`);' "$package_dir")"
  if npm view "$spec" version "$npmjs_registry_config" >/dev/null 2>&1; then
    echo "npm package already exists; skipping ${spec}"
    printf "%s\t%s\n" "$spec" "already exists" >> "$summary_file"
    return
  fi

  if npm publish "$package_dir" --access public "$npmjs_registry_config"; then
    printf "%s\t%s\n" "$spec" "published" >> "$summary_file"
  else
    printf "%s\t%s\n" "$spec" "failed" >> "$summary_file"
    return 1
  fi
}

publish_package "$publish_root/packages/binaries/darwin-arm64"
publish_package "$publish_root/packages/binaries/darwin-x64"
publish_package "$publish_root/packages/binaries/linux-arm64"
publish_package "$publish_root/packages/binaries/linux-x64"
publish_package "$publish_root/packages/node"
