#!/usr/bin/env bash
set -euo pipefail

tag="${1:?Usage: publish-release.sh vX.Y.Z}"

if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
	echo "release tag must be strict vX.Y.Z: $tag" >&2
	exit 1
fi

bash .github/scripts/verify-release-tag-target.sh "$tag" "${GITHUB_SHA:?}"

assets=(
	gate-darwin-amd64
	gate-darwin-arm64
	gate-linux-amd64
	gate-linux-arm64
	checksums.txt
)

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
	state="$(gh release view "$tag" --json isDraft,isPrerelease --jq '[.isDraft, .isPrerelease] | @tsv')"
	IFS=$'\t' read -r is_draft is_prerelease <<<"$state"
	if [ "$is_draft" = "true" ] || [ "$is_prerelease" = "true" ]; then
		echo "stable release tag has non-final GitHub release state (draft=${is_draft}, prerelease=${is_prerelease}): $tag" >&2
		exit 1
	fi
	existing="$(gh release view "$tag" --json assets --jq '.assets[].name')"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	missing=()
	for asset in "${assets[@]}"; do
		if printf '%s\n' "$existing" | grep -Fx "$asset" >/dev/null; then
			gh release download "$tag" --pattern "$asset" --dir "$tmp"
			if ! cmp -s "$asset" "$tmp/$asset"; then
				echo "release asset differs from local file; refusing to replace tagged artifact: $asset" >&2
				exit 1
			fi
			continue
		fi
		missing+=("$asset")
	done
	for asset in "${missing[@]}"; do
		gh release upload "$tag" "$asset"
	done
else
  gh release create "$tag" \
		"${assets[@]}" \
    --title "$tag" \
    --notes "$notes" \
    --verify-tag
fi
