#!/usr/bin/env bash
set -euo pipefail

tag="${1:?Usage: publish-release.sh vX.Y.Z}"

latest_published_tag() {
  local error
  local status

  error="$(mktemp)"
  status=0
  gh api "repos/${GITHUB_REPOSITORY:?}/releases/latest" --jq .tag_name 2>"$error" || status=$?
  if [ "$status" -ne 0 ]; then
    if grep -Eiq '(^|[^0-9])404([^0-9]|$)|not found|could not resolve to a release' "$error"; then
      rm -f "$error"
      return 0
    fi
    cat "$error" >&2
    rm -f "$error"
    return "$status"
  fi
  rm -f "$error"
}

format_commits() {
  local range="$1"

  if [ -z "$range" ]; then
    git log --oneline --no-decorate "$tag"
  else
    git log --oneline --no-decorate "$range"
  fi
}

tag_notes() {
  if ! git cat-file -e "${tag}^{tag}" 2>/dev/null; then
    return 0
  fi
  git tag -l --format='%(contents:subject)%0a%0a%(contents:body)' "$tag"
}

notes="$(tag_notes)"
[ -n "$(printf '%s' "$notes" | tr -d '[:space:]')" ] || notes=""

base=""
range=""
if [ -z "$notes" ]; then
  if ! base="$(latest_published_tag)"; then
    echo "Failed to read latest published GitHub release for release notes." >&2
    exit 1
  fi
  if [ -n "$base" ] && git rev-parse -q --verify "refs/tags/$base" >/dev/null; then
    range="${base}..${tag}"
  fi

  notes="$(printf 'Release %s\n\n%s' "$tag" "$(format_commits "$range" | sed 's/^/- /')")"
fi

if gh release view "$tag" >/dev/null 2>&1; then
  gh release upload "$tag" gate-* checksums.txt --clobber
else
  gh release create "$tag" \
    gate-darwin-amd64 gate-darwin-arm64 gate-linux-amd64 gate-linux-arm64 checksums.txt \
    --title "$tag" \
    --notes "$notes" \
    --verify-tag
fi
