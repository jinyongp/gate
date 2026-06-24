#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_STEP_SUMMARY:?}"
: "${GITHUB_REF_NAME:?}"
: "${GITHUB_SHA:?}"
: "${RELEASE_TAG:=}"
: "${TAG_TYPE:?}"
: "${TAG_TARGET:=}"
: "${TAG_ON_MAIN:?}"
: "${RELEASE_STATUS:?}"
: "${RELEASE_TAG_JOB_RESULT:?}"
: "${CHECK_JOB_RESULT:?}"
: "${BUILD_JOB_RESULT:?}"
: "${HOMEBREW_TAP_JOB_RESULT:?}"
: "${NPM_PUBLISH_JOB_RESULT:?}"

{
  echo "## Release summary"
  echo
  echo "- Ref: \`${GITHUB_REF_NAME}\`"
  echo "- Commit: \`${GITHUB_SHA}\`"
  if [ -n "$RELEASE_TAG" ]; then
    echo "- Release tag: \`${RELEASE_TAG}\`"
    echo "- Tag type: \`${TAG_TYPE}\`"
    echo "- Tag target: \`${TAG_TARGET}\`"
    echo "- Tag on main: \`${TAG_ON_MAIN}\`"
    echo "- Release before publish: \`${RELEASE_STATUS}\`"
  else
    echo "- Release tag: none"
  fi
  echo
  echo "| Job | Result |"
  echo "| --- | --- |"
  echo "| release tag | ${RELEASE_TAG_JOB_RESULT} |"
  echo "| check | ${CHECK_JOB_RESULT} |"
  echo "| build | ${BUILD_JOB_RESULT} |"
  echo "| homebrew tap | ${HOMEBREW_TAP_JOB_RESULT} |"
  echo "| npm publish | ${NPM_PUBLISH_JOB_RESULT} |"
} >> "$GITHUB_STEP_SUMMARY"
