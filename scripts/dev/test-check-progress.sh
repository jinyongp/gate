#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

mkdir -p "$test_root/scripts/dev" "$test_root/bin"
cp scripts/dev/check.sh "$test_root/scripts/dev/check.sh"

for script in docs-check.sh fmt-check.sh vet.sh cover.sh lint.sh vuln.sh check-scripts.sh; do
  cat >"$test_root/scripts/dev/$script" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [ "$(basename "$0")" = "${GATE_CHECK_FAIL_STEP:-}" ]; then
  echo "injected check failure" >&2
  exit 23
fi
EOF
  chmod +x "$test_root/scripts/dev/$script"
done

cat >"$test_root/bin/pnpm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
test "${1:-}" = "node:check"
EOF
chmod +x "$test_root/bin/pnpm"

success_output="$(
  cd "$test_root"
  PATH="$test_root/bin:$PATH" scripts/dev/check.sh
)"

expected_labels=(
  "Documentation boundaries"
  "Go formatting"
  "Go vet"
  "Go tests and coverage"
  "Node checks"
  "Go lint (Darwin + Linux)"
  "Vulnerability scan"
  "Shell and workflow checks"
)
for index in "${!expected_labels[@]}"; do
  step="$((index + 1))"
  label="${expected_labels[$index]}"
  grep -F "[$step/8] $label" <<<"$success_output" >/dev/null
  grep -F "ok: [$step/8] $label (" <<<"$success_output" >/dev/null
done

set +e
failure_output="$(
  cd "$test_root"
  GATE_CHECK_FAIL_STEP=vuln.sh PATH="$test_root/bin:$PATH" scripts/dev/check.sh 2>&1
)"
failure_status=$?
set -e

test "$failure_status" -eq 23
grep -F "[7/8] Vulnerability scan" <<<"$failure_output" >/dev/null
grep -F "error: [7/8] Vulnerability scan (" <<<"$failure_output" >/dev/null
grep -F "injected check failure" <<<"$failure_output" >/dev/null
if grep -F "[8/8]" <<<"$failure_output" >/dev/null; then
  echo "check runner continued after a failed step" >&2
  exit 1
fi
