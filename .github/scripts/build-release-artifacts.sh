#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_OUTPUT:?}"
: "${GATE_DEV:?}"

if [ ! -x "$GATE_DEV" ]; then
  echo "gate-dev is not executable: $GATE_DEV" >&2
  exit 1
fi

version="${1:-}"
if [ -z "$version" ]; then
  version="$(git describe --tags --always --dirty)"
fi

echo "version=${version}" >> "$GITHUB_OUTPUT"
"$GATE_DEV" build-all "$version"
