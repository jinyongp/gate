#!/usr/bin/env bash
set -euo pipefail

tag="${1:?Usage: publish-release.sh vX.Y.Z}"

# Use the annotated tag message as the release notes. subject+body excludes any
# GPG signature block; fall back to a bare line for lightweight tags.
notes="$(git tag -l --format='%(contents:subject)%0a%0a%(contents:body)' "$tag")"
[ -n "$(echo "$notes" | tr -d '[:space:]')" ] || notes="Release $tag"

if gh release view "$tag" >/dev/null 2>&1; then
  gh release upload "$tag" prx-* checksums.txt --clobber
else
  gh release create "$tag" \
    prx-darwin-amd64 prx-darwin-arm64 prx-linux-amd64 prx-linux-arm64 checksums.txt \
    --title "$tag" \
    --notes "$notes" \
    --verify-tag
fi
