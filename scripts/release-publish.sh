#!/usr/bin/env bash
set -euo pipefail

TAG_INPUT="${1:-}"
TAG_INPUT="${TAG_INPUT#tag=}"

if [ -z "$TAG_INPUT" ]; then
  echo "Usage: scripts/release-publish.sh <vX.Y.Z>"
  exit 1
fi

if [[ ! "$TAG_INPUT" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Patch tag must be semver form: vX.Y.Z (for example: v1.2.3)"
  exit 1
fi

if [ "$(git symbolic-ref --short HEAD)" != "main" ]; then
  echo "release-publish must run on branch 'main'."
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "Working tree is dirty. Commit or stash changes first."
  exit 1
fi

git push origin main

VERSION="${TAG_INPUT#v}"
MAJOR="${VERSION%%.*}"
REST="${VERSION#*.}"
MINOR="${REST%%.*}"
MINOR_TAG="v${MAJOR}.${MINOR}"
MAJOR_TAG="v${MAJOR}"
PATCH_TAG="${TAG_INPUT}"
TARGET_SHA="$(git rev-parse origin/main)"

if git rev-parse -q --verify "refs/tags/$PATCH_TAG" >/dev/null; then
  echo "Patch tag already exists: $PATCH_TAG"
  exit 1
fi

if git ls-remote --exit-code --tags origin "refs/tags/$PATCH_TAG" >/dev/null 2>&1; then
  echo "Remote patch tag already exists: $PATCH_TAG"
  exit 1
fi

git tag -a "$PATCH_TAG" -m "Release $PATCH_TAG" "$TARGET_SHA"
for alias_tag in "$MINOR_TAG" "$MAJOR_TAG"; do
  if git rev-parse -q --verify "refs/tags/$alias_tag" >/dev/null; then
    git tag -d "$alias_tag"
  fi
  git tag -a "$alias_tag" -m "Release $alias_tag" "$TARGET_SHA"
done

git push origin "$PATCH_TAG" "$MINOR_TAG" "$MAJOR_TAG" --force
