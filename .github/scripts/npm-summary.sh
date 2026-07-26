#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_STEP_SUMMARY:?}"
: "${GITHUB_REF_NAME:?}"
: "${GITHUB_SHA:?}"
: "${VERSION_TAG:?}"
: "${RELEASE_TARGET:?}"
: "${RELEASE_OBJECT:?}"
: "${COREPACK_OUTCOME:?}"
: "${INSTALL_OUTCOME:?}"
: "${BUILD_BINARIES_OUTCOME:?}"
: "${PUBLISH_NPM_OUTCOME:?}"

summary_file="${NPM_PUBLISH_SUMMARY:-npm-publish-summary.tsv}"

{
  echo "## npm publish"
  echo
  echo "- Ref: \`${GITHUB_REF_NAME}\`"
  echo "- Workflow commit: \`${GITHUB_SHA}\`"
  echo "- Release target: \`${RELEASE_TARGET}\`"
  echo "- Release tag object: \`${RELEASE_OBJECT}\`"
  echo "- Version: \`${VERSION_TAG}\`"
  echo
  echo "| Step | Result |"
  echo "| --- | --- |"
  echo "| enable corepack | ${COREPACK_OUTCOME} |"
  echo "| install | ${INSTALL_OUTCOME} |"
  echo "| build release binaries | ${BUILD_BINARIES_OUTCOME} |"
  echo "| publish npm packages | ${PUBLISH_NPM_OUTCOME} |"
  echo
  echo "### Packages"
  echo
  if [ -s "$summary_file" ]; then
    echo "| Package | Result |"
    echo "| --- | --- |"
    while IFS=$'\t' read -r package result; do
      echo "| \`${package}\` | ${result} |"
    done < "$summary_file"
  else
    echo "_No package publish results recorded._"
  fi
} >> "$GITHUB_STEP_SUMMARY"
