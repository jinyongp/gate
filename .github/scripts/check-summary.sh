#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_STEP_SUMMARY:?}"
: "${GITHUB_REF_NAME:?}"
: "${GITHUB_SHA:?}"
: "${SOURCE_SHA:=$GITHUB_SHA}"
: "${RUNNER_OS_NAME:?}"
: "${GOFMT_OUTCOME:?}"
: "${VET_OUTCOME:?}"
: "${GOLANGCI_LINT_OUTCOME:?}"
: "${GOVULNCHECK_OUTCOME:?}"
: "${SCRIPTS_OUTCOME:?}"
: "${LINUX_LOW_PORT_OUTCOME:?}"
: "${NODE_CHECK_OUTCOME:?}"
: "${TEST_OUTCOME:?}"

{
  echo "## CI check (${RUNNER_OS_NAME})"
  echo
  echo "- Ref: \`${GITHUB_REF_NAME}\`"
  echo "- Workflow commit: \`${GITHUB_SHA}\`"
  echo "- Source commit: \`${SOURCE_SHA}\`"
  echo "- Go: \`$(go version | awk '{print $3}')\`"
  echo
  echo "| Step | Result |"
  echo "| --- | --- |"
  echo "| gofmt | ${GOFMT_OUTCOME} |"
  echo "| vet | ${VET_OUTCOME} |"
  echo "| golangci-lint | ${GOLANGCI_LINT_OUTCOME} |"
  echo "| govulncheck | ${GOVULNCHECK_OUTCOME} |"
  echo "| scripts | ${SCRIPTS_OUTCOME} |"
  echo "| Linux low ports | ${LINUX_LOW_PORT_OUTCOME} |"
  echo "| node check | ${NODE_CHECK_OUTCOME} |"
  echo "| test + coverage | ${TEST_OUTCOME} |"
} >> "$GITHUB_STEP_SUMMARY"
