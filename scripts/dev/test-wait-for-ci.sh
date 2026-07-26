#!/usr/bin/env bash
set -euo pipefail

test_tmp="$(mktemp -d)"
trap 'rm -rf "$test_tmp"' EXIT
mkdir -p "$test_tmp/bin"

cat > "$test_tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "$*" in
  *"repos/test/repo/actions/workflows/ci.yml/runs"*"head_sha=0123456789abcdef0123456789abcdef01234567"*"event=push"*) ;;
  *)
    echo "unexpected gh arguments: $*" >&2
    exit 2
    ;;
esac

case "${FAKE_GH_SCENARIO:?}" in
  success-after-wait)
    count=0
    if [ -f "$FAKE_GH_COUNT_FILE" ]; then
      count="$(cat "$FAKE_GH_COUNT_FILE")"
    fi
    count=$((count + 1))
    printf '%s\n' "$count" > "$FAKE_GH_COUNT_FILE"
    case "$count" in
      1) ;;
      2) printf '101\tin_progress\t\thttps://example.test/101\n' ;;
      *) printf '101\tcompleted\tsuccess\thttps://example.test/101\n' ;;
    esac
    ;;
  failure)
    printf '102\tcompleted\tfailure\thttps://example.test/102\n'
    ;;
  api-error)
    exit 3
    ;;
  *)
    echo "unknown fake gh scenario" >&2
    exit 2
    ;;
esac
EOF
chmod 0700 "$test_tmp/bin/gh"

cat > "$test_tmp/bin/sleep" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod 0700 "$test_tmp/bin/sleep"

sha="0123456789abcdef0123456789abcdef01234567"
common_env=(
  "PATH=$test_tmp/bin:$PATH"
  "GH_REPO=test/repo"
  "CI_WAIT_TIMEOUT_SECONDS=10"
  "CI_WAIT_POLL_SECONDS=1"
  "FAKE_GH_COUNT_FILE=$test_tmp/count"
)

env "${common_env[@]}" FAKE_GH_SCENARIO=success-after-wait \
  bash .github/scripts/wait-for-ci.sh "$sha" >/dev/null
test "$(cat "$test_tmp/count")" = "3"

if env "${common_env[@]}" FAKE_GH_SCENARIO=failure \
  bash .github/scripts/wait-for-ci.sh "$sha" >/dev/null 2>&1; then
  echo "wait-for-ci accepted a failed CI run" >&2
  exit 1
fi

if env "${common_env[@]}" FAKE_GH_SCENARIO=api-error \
  bash .github/scripts/wait-for-ci.sh "$sha" >/dev/null 2>&1; then
  echo "wait-for-ci ignored a GitHub API failure" >&2
  exit 1
fi

if env "${common_env[@]}" FAKE_GH_SCENARIO=success-after-wait \
  bash .github/scripts/wait-for-ci.sh invalid >/dev/null 2>&1; then
  echo "wait-for-ci accepted an invalid commit SHA" >&2
  exit 1
fi
