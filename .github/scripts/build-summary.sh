#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_STEP_SUMMARY:?}"
: "${GITHUB_REF_NAME:?}"
: "${GITHUB_SHA:?}"
: "${BUILD_VERSION:?}"
: "${RELEASE_TAG:=}"
: "${TAG_TYPE:?}"
: "${TAG_TARGET:=}"
: "${TAG_OBJECT:=}"
: "${TAG_ON_MAIN:?}"
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
    echo "- Tag object: \`${TAG_OBJECT}\`"
    echo "- Tag on main: \`${TAG_ON_MAIN}\`"
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
  if ls gate-* >/dev/null 2>&1; then
    echo
    echo "| File | Size | SHA-256 |"
    echo "| --- | ---: | --- |"
    for file in gate-darwin-amd64 gate-darwin-arm64 gate-linux-amd64 gate-linux-arm64; do
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
