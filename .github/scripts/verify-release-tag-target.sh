#!/usr/bin/env bash
set -euo pipefail

tag="${1:?Usage: verify-release-tag-target.sh vX.Y.Z expected-sha}"
expected="${2:?Usage: verify-release-tag-target.sh vX.Y.Z expected-sha}"

if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || [[ ! "$expected" =~ ^[0-9a-fA-F]{40}$ ]]; then
	echo "invalid release tag or expected SHA" >&2
	exit 1
fi

remote_target="$(git ls-remote origin "refs/tags/${tag}^{}" | awk 'NR == 1 {print $1}')"
if [ -z "$remote_target" ]; then
	remote_target="$(git ls-remote origin "refs/tags/${tag}" | awk 'NR == 1 {print $1}')"
fi
if [ -z "$remote_target" ]; then
	echo "release tag is missing from origin: $tag" >&2
	exit 1
fi
if [ "$remote_target" != "$expected" ]; then
	echo "release tag target moved: tag=${remote_target} expected=${expected}" >&2
	exit 1
fi
