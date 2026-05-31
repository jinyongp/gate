#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_STEP_SUMMARY:?}"
: "${GITHUB_REF_NAME:?}"
: "${GITHUB_SHA:?}"
: "${BUILD_VERSION:?}"
: "${RELEASE_TAG:=}"
: "${TAG_TYPE:?}"
: "${TAG_TARGET:=}"
: "${RELEASE_STATUS:?}"
: "${DETECT_TAG_OUTCOME:?}"
: "${BUILD_OUTCOME:?}"
: "${UPLOAD_ARTIFACT_OUTCOME:?}"
: "${CHECKSUMS_OUTCOME:?}"
: "${PUBLISH_RELEASE_OUTCOME:?}"

{
  echo "## Build"
  echo
  echo "- Ref: \`${GITHUB_REF_NAME}\`"
  echo "- Commit: \`${GITHUB_SHA}\`"
  echo "- Version: \`${BUILD_VERSION}\`"
  if [ -n "$RELEASE_TAG" ]; then
    echo "- Release tag: \`${RELEASE_TAG}\`"
    echo "- Tag type: \`${TAG_TYPE}\`"
    echo "- Tag target: \`${TAG_TARGET}\`"
    echo "- Release before publish: \`${RELEASE_STATUS}\`"
  else
    echo "- Release tag: none"
  fi
  echo
  echo "| Step | Result |"
  echo "| --- | --- |"
  echo "| detect release tag | ${DETECT_TAG_OUTCOME} |"
  echo "| build | ${BUILD_OUTCOME} |"
  echo "| upload artifact | ${UPLOAD_ARTIFACT_OUTCOME} |"
  echo "| checksums | ${CHECKSUMS_OUTCOME} |"
  echo "| publish release | ${PUBLISH_RELEASE_OUTCOME} |"
  echo
  echo "### Artifacts"
  if ls prx-* >/dev/null 2>&1; then
    echo
    echo "| File | Size | SHA-256 |"
    echo "| --- | ---: | --- |"
    for file in prx-darwin-amd64 prx-darwin-arm64 prx-linux-amd64 prx-linux-arm64; do
      [ -f "$file" ] || continue
      size="$(du -h "$file" | awk '{print $1}')"
      sha="-"
      if [ -f checksums.txt ]; then
        sha="$(awk -v file="$file" '$2 == file { print $1 }' checksums.txt)"
        [ -n "$sha" ] || sha="-"
      fi
      echo "| \`$file\` | $size | \`$sha\` |"
    done
  else
    echo
    echo "_No artifacts created._"
  fi
} >> "$GITHUB_STEP_SUMMARY"
