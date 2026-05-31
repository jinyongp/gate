#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_OUTPUT:?}"

tag="$(git tag --points-at HEAD --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n 1 || true)"
type="none"
target=""
release="not-applicable"
release_error=""

if [ -n "$tag" ]; then
  if git cat-file -e "${tag}^{tag}" 2>/dev/null; then
    type="annotated"
    target="$(git rev-parse "${tag}^{commit}")"
  else
    type="lightweight"
    target="$(git rev-parse "$tag")"
  fi

  release_error="$(mktemp)"
  if gh release view "$tag" >/dev/null 2>"$release_error"; then
    release="existing"
  elif grep -Eiq 'not found|could not resolve to a release' "$release_error"; then
    release="missing"
  else
    release="unknown"
  fi
  rm -f "$release_error"
fi

{
  echo "tag=${tag}"
  echo "type=${type}"
  echo "target=${target}"
  echo "release=${release}"
} >> "$GITHUB_OUTPUT"
