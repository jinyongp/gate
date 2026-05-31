#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_OUTPUT:?}"

version="${1:-}"
if [ -z "$version" ]; then
  version="$(git describe --tags --always --dirty)"
fi

echo "version=${version}" >> "$GITHUB_OUTPUT"
scripts/release/build-prx.sh "$version"
