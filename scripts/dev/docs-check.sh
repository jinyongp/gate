#!/usr/bin/env bash
set -euo pipefail

spec="${DOCS_CHECK_SPEC:-docs/spec.md}"
failed=0

if [[ ! -f "$spec" ]]; then
  printf 'docs-check: missing spec file: %s\n' "$spec" >&2
  exit 1
fi

check_absent() {
  local message="$1"
  local pattern="$2"
  local matches
  local status=0

  matches="$(grep -En -- "$pattern" "$spec")" || status=$?

  if ((status == 0)); then
    printf 'docs-check: %s\n' "$message" >&2
    printf '%s\n' "$matches" >&2
    failed=1
  elif ((status > 1)); then
    printf 'docs-check: grep failed for pattern: %s\n' "$pattern" >&2
    exit "$status"
  fi
}

check_absent 'docs/spec.md must not contain shell command fences; put command examples in docs/usage.md.' '^```bash$'
check_absent 'docs/spec.md must not contain exact gate command invocations; put command syntax in docs/usage.md.' '`gate [a-z][a-z0-9-]*'
check_absent 'docs/spec.md must not contain exact CLI long flags; put flags in docs/usage.md.' '`--[a-z][a-z0-9-]*'
check_absent 'docs/spec.md must not contain numeric exit-code references; put exit codes in docs/usage.md.' 'exit code `?[0-9]'
check_absent 'docs/spec.md must not contain exit-code mapping table rows; put exit codes in docs/usage.md.' '^\| `[0-9]` \|'
check_absent 'docs/spec.md must not contain auth status value lists; put output semantics in docs/usage.md.' 'off`, `active'
check_absent 'docs/spec.md must not mention the AUTH column; put output fields in docs/usage.md.' 'AUTH column'
check_absent 'docs/spec.md must not mention auth_status; put output fields in docs/usage.md.' 'auth_status'
check_absent 'docs/spec.md must not document JSON-mode error details; put output semantics in docs/usage.md.' 'JSON-mode errors'
check_absent 'docs/spec.md must not contain text output examples; put output examples in docs/usage.md.' '^removed '
check_absent 'docs/spec.md must not contain JSON output examples; put output examples in docs/usage.md.' '^\{"scope":'

exit "$failed"
