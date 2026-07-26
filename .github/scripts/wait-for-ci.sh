#!/usr/bin/env bash
set -euo pipefail

sha="${1:-}"
if [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "usage: wait-for-ci.sh <40-character commit SHA>" >&2
  exit 2
fi

repository="${GH_REPO:-${GITHUB_REPOSITORY:-}}"
if [ -z "$repository" ]; then
  echo "GH_REPO or GITHUB_REPOSITORY is required" >&2
  exit 2
fi

workflow="${CI_WORKFLOW_FILE:-ci.yml}"
timeout_seconds="${CI_WAIT_TIMEOUT_SECONDS:-1800}"
poll_seconds="${CI_WAIT_POLL_SECONDS:-10}"

if [[ ! "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] ||
  [[ ! "$poll_seconds" =~ ^[1-9][0-9]*$ ]]; then
  echo "CI wait timeout and poll interval must be positive integers" >&2
  exit 2
fi

deadline=$((SECONDS + timeout_seconds))
last_reported=""

while ((SECONDS <= deadline)); do
  run="$(
    gh api --method GET \
      -H "Accept: application/vnd.github+json" \
      "repos/${repository}/actions/workflows/${workflow}/runs" \
      -f "head_sha=${sha}" \
      -f event=push \
      -f per_page=100 \
      --jq '
        .workflow_runs
        | sort_by(.created_at)
        | last
        | select(. != null)
        | [.id, .status, (.conclusion // ""), .html_url]
        | @tsv
      '
  )"

  if [ -z "$run" ]; then
    report="waiting for CI run for ${sha}"
  else
    IFS=$'\t' read -r run_id status conclusion run_url <<<"$run"
    report="CI run ${run_id}: ${status}${conclusion:+/${conclusion}}"
    case "$status" in
      completed)
        if [ "$conclusion" = "success" ]; then
          echo "${report} (${run_url})"
          exit 0
        fi
        echo "${report} (${run_url})" >&2
        exit 1
        ;;
      queued|in_progress|pending|requested|waiting)
        ;;
      *)
        echo "unexpected CI run status ${status}: ${run_url}" >&2
        exit 1
        ;;
    esac
  fi

  if [ "$report" != "$last_reported" ]; then
    echo "$report"
    last_reported="$report"
  fi
  if ((SECONDS >= deadline)); then
    break
  fi
  sleep "$poll_seconds"
done

echo "timed out waiting for successful CI for ${sha}" >&2
exit 1
