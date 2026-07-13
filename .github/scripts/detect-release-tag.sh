#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_OUTPUT:?}"

tag=""
type="none"
target=""
release="not-applicable"
release_error=""
on_main="false"

if [ "${GITHUB_REF_TYPE:-}" = "tag" ]; then
  tag="${GITHUB_REF_NAME:-}"
else
	tag="$(git tag --points-at HEAD --list 'v*' --sort=-v:refname | while IFS= read -r candidate; do
		if [[ "$candidate" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
			if [ -z "${found_tag:-}" ]; then
				printf '%s\n' "$candidate"
				found_tag="$candidate"
			fi
		fi
	done)"
fi

if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  tag=""
fi

if [ -n "$tag" ]; then
  if git cat-file -e "${tag}^{tag}" 2>/dev/null; then
    type="annotated"
    target="$(git rev-parse "${tag}^{commit}")"
  else
    type="lightweight"
    target="$(git rev-parse "$tag")"
  fi
	if [ "${GITHUB_REF_TYPE:-}" = "tag" ] && [ "$target" != "$(git rev-parse HEAD)" ]; then
		echo "release tag target does not match checked-out HEAD: tag=${target} head=$(git rev-parse HEAD)" >&2
		exit 1
	fi
	latest_stable="$(git tag --list 'v*' | grep -E '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' | sort -V | tail -n 1)"
	if [ -n "$latest_stable" ] && [ "$tag" != "$latest_stable" ]; then
		echo "refusing to publish older tag ${tag}; highest stable tag is ${latest_stable}" >&2
		exit 1
	fi

  git fetch --force --quiet origin refs/heads/main:refs/remotes/origin/main
  if git merge-base --is-ancestor "$target" refs/remotes/origin/main; then
    on_main="true"
  fi

  release_error="$(mktemp)"
  if gh release view "$tag" >/dev/null 2>"$release_error"; then
	state="$(gh release view "$tag" --json isDraft,isPrerelease --jq '[.isDraft, .isPrerelease] | @tsv')"
	IFS=$'\t' read -r is_draft is_prerelease <<<"$state"
	if [ "$is_draft" = "true" ] || [ "$is_prerelease" = "true" ]; then
		echo "stable release tag has non-final GitHub release state (draft=${is_draft}, prerelease=${is_prerelease}): $tag" >&2
		rm -f "$release_error"
		exit 1
	fi
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
  echo "on_main=${on_main}"
} >> "$GITHUB_OUTPUT"
