#!/usr/bin/env bash
set -euo pipefail

check_output="$(mktemp)"
trap 'rm -f "$check_output"' EXIT

run_check() {
  local index="$1"
  local label="$2"
  local started_at="$SECONDS"
  local status
  shift 2

  printf '[%s/8] %s\n' "$index" "$label"
  if "$@" >"$check_output" 2>&1; then
    printf 'ok: [%s/8] %s (%ss)\n' "$index" "$label" "$((SECONDS - started_at))"
    return 0
  else
    status=$?
  fi

  printf 'error: [%s/8] %s (%ss)\n' "$index" "$label" "$((SECONDS - started_at))" >&2
  cat "$check_output" >&2
  return "$status"
}

run_check 1 "Documentation boundaries" scripts/dev/docs-check.sh
run_check 2 "Go formatting" scripts/dev/fmt-check.sh
run_check 3 "Go vet" scripts/dev/vet.sh
run_check 4 "Go tests and coverage" scripts/dev/cover.sh
run_check 5 "Node checks" pnpm node:check
run_check 6 "Go lint (Darwin + Linux)" scripts/dev/lint.sh
run_check 7 "Vulnerability scan" scripts/dev/vuln.sh
run_check 8 "Shell and workflow checks" scripts/dev/check-scripts.sh
