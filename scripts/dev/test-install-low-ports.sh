#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

fake_bin="$test_root/fake-bin"
mkdir -p "$fake_bin"

cat >"$fake_bin/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
  -s) printf 'Linux\n' ;;
  -m) printf 'x86_64\n' ;;
  *) exit 1 ;;
esac
EOF

cat >"$fake_bin/curl" <<'EOF'
#!/bin/sh
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      continue
      ;;
    http://*|https://*)
      url="$1"
      ;;
  esac
  shift
done
case "$url" in
  https://api.github.com/*)
    printf '{"assets":[{"browser_download_url":"https://example.test/gate-linux-amd64"},{"browser_download_url":"https://example.test/checksums.txt"}]}\n'
    ;;
  https://example.test/gate-linux-amd64)
    cp "$GATE_TEST_ASSET" "$output"
    ;;
  https://example.test/checksums.txt)
    printf 'test-checksum  gate-linux-amd64\n' >"$output"
    ;;
  *)
    exit 1
    ;;
esac
EOF

cat >"$fake_bin/sha256sum" <<'EOF'
#!/bin/sh
printf 'test-checksum  %s\n' "$1"
EOF

cat >"$fake_bin/getcap" <<'EOF'
#!/bin/sh
if [ "${GATE_TEST_GETCAP_STATE:-missing}" = "configured" ]; then
  printf '%s cap_net_bind_service=ep\n' "$1"
fi
EOF

chmod +x "$fake_bin/uname" "$fake_bin/curl" "$fake_bin/sha256sum" "$fake_bin/getcap"

asset="$test_root/gate-linux-amd64"
cat >"$asset" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$GATE_TEST_SETUP_LOG"
if [ -n "${GATE_TEST_SETUP_DELAY:-}" ]; then
  sleep "$GATE_TEST_SETUP_DELAY"
fi
if [ -n "${GATE_TEST_SETUP_MESSAGE:-}" ]; then
  printf '%s\n' "$GATE_TEST_SETUP_MESSAGE" >&2
fi
exit "${GATE_TEST_SETUP_EXIT:-0}"
EOF
chmod +x "$asset"

run_installer() {
  case_root="$1"
  shift
  mkdir -p "$case_root/home" "$case_root/bin"
  env \
    PATH="$fake_bin:/usr/bin:/bin" \
    HOME="$case_root/home" \
    SHELL=/bin/sh \
    GATE_BIN_DIR="$case_root/bin" \
    GATE_TEST_ASSET="$asset" \
    GATE_TEST_SETUP_LOG="$case_root/setup.log" \
    GATE_INSTALL_TEST_GETCAP="$fake_bin/getcap" \
    "$@" \
    sh scripts/install.sh
}

run_installer_pty() {
  case_root="$1"
  input="$2"
  shift 2
  mkdir -p "$case_root/home" "$case_root/bin"
  env \
    PATH="$fake_bin:/usr/bin:/bin" \
    HOME="$case_root/home" \
    SHELL=/bin/sh \
    GATE_BIN_DIR="$case_root/bin" \
    GATE_TEST_ASSET="$asset" \
    GATE_TEST_SETUP_LOG="$case_root/setup.log" \
    GATE_INSTALL_TEST_GETCAP="$fake_bin/getcap" \
    GATE_TEST_PTY_INPUT="$input" \
    "$@" \
    python3 - <<'PY'
import errno
import os
import pty

pid, fd = pty.fork()
if pid == 0:
    os.execvpe("sh", ["sh", "scripts/install.sh"], os.environ)

os.write(fd, os.environ["GATE_TEST_PTY_INPUT"].encode())
while True:
    try:
        data = os.read(fd, 4096)
    except OSError as error:
        if error.errno == errno.EIO:
            break
        raise
    if not data:
        break
    os.write(1, data)

_, status = os.waitpid(pid, 0)
raise SystemExit(os.waitstatus_to_exitcode(status))
PY
}

pty_is_usable() {
  python3 - <<'PY'
import os
import pty

pid, fd = pty.fork()
if pid == 0:
    os.execv("/bin/sh", ["sh", "-c", "test -t 1 && test -r /dev/tty && test -w /dev/tty"])
_, status = os.waitpid(pid, 0)
os.close(fd)
raise SystemExit(os.waitstatus_to_exitcode(status))
PY
}

fresh="$test_root/fresh"
fresh_output="$fresh/output.log"
mkdir -p "$fresh"
if ! run_installer "$fresh" >"$fresh_output" 2>&1; then
  cat "$fresh_output" >&2
  exit 1
fi
test -x "$fresh/bin/gate"
test ! -e "$fresh/setup.log"
grep -F "gate daemon setup" "$fresh_output" >/dev/null

interactive_accept="$test_root/interactive-accept"
interactive_decline="$test_root/interactive-decline"
interactive_setup_failure="$test_root/interactive-setup-failure"
mkdir -p \
  "$interactive_accept/bin" \
  "$interactive_decline/bin" \
  "$interactive_setup_failure/bin"
if pty_is_usable; then
if ! run_installer_pty "$interactive_accept" $'\nn\n' >"$interactive_accept/output.log" 2>&1; then
  cat "$interactive_accept/output.log" >&2
  exit 1
fi
if [ ! -e "$interactive_accept/setup.log" ]; then
  cat "$interactive_accept/output.log" >&2
  echo "interactive installer did not run low-port setup" >&2
  exit 1
fi
test "$(cat "$interactive_accept/setup.log")" = "daemon setup --yes"
grep -F "configured Linux low-port capability" "$interactive_accept/output.log" >/dev/null

if ! run_installer_pty "$interactive_decline" $'n\nn\n' >"$interactive_decline/output.log" 2>&1; then
  cat "$interactive_decline/output.log" >&2
  exit 1
fi
test ! -e "$interactive_decline/setup.log"
grep -F "Linux low-port setup skipped" "$interactive_decline/output.log" >/dev/null

if ! run_installer_pty "$interactive_setup_failure" $'\nn\n' \
  GATE_TEST_SETUP_EXIT=1 \
  GATE_TEST_SETUP_MESSAGE="Operation not supported" \
  >"$interactive_setup_failure/output.log" 2>&1; then
  cat "$interactive_setup_failure/output.log" >&2
  exit 1
fi
test -x "$interactive_setup_failure/bin/gate"
grep -F "Linux low-port setup failed" "$interactive_setup_failure/output.log" >/dev/null
grep -F "gate daemon setup" "$interactive_setup_failure/output.log" >/dev/null
elif [ "${GATE_REQUIRE_INSTALL_PTY_TEST:-0}" = "1" ]; then
  echo "interactive installer PTY fixture is required but unavailable" >&2
  exit 1
else
  echo "skip: interactive installer PTY fixture unavailable"
fi

upgrade="$test_root/upgrade"
mkdir -p "$upgrade/bin"
printf '#!/bin/sh\nprintf old\n' >"$upgrade/bin/gate"
chmod +x "$upgrade/bin/gate"
if ! run_installer "$upgrade" GATE_TEST_GETCAP_STATE=configured >"$upgrade/output.log" 2>&1; then
  cat "$upgrade/output.log" >&2
  exit 1
fi
grep -F "daemon setup --yes" "$upgrade/setup.log" >/dev/null
test "$(cat "$upgrade/setup.log")" = "daemon setup --yes"
test "$(head -n 1 "$upgrade/bin/gate")" = "#!/bin/sh"
if rg -F "printf old" "$upgrade/bin/gate" >/dev/null; then
  echo "configured upgrade kept the old binary" >&2
  exit 1
fi

rollback="$test_root/rollback"
mkdir -p "$rollback/bin"
printf '#!/bin/sh\nprintf old\n' >"$rollback/bin/gate"
chmod +x "$rollback/bin/gate"
if run_installer "$rollback" GATE_TEST_GETCAP_STATE=configured GATE_TEST_SETUP_EXIT=1 >"$rollback/output.log" 2>&1; then
  echo "configured upgrade succeeded after capability setup failure" >&2
  exit 1
fi
grep -F "printf old" "$rollback/bin/gate" >/dev/null
grep -F "Restored the previous gate binary." "$rollback/output.log" >/dev/null

interrupted="$test_root/interrupted"
mkdir -p "$interrupted/bin"
printf '#!/bin/sh\nprintf old\n' >"$interrupted/bin/gate"
chmod +x "$interrupted/bin/gate"
if run_installer "$interrupted" \
  GATE_TEST_GETCAP_STATE=configured \
  GATE_INSTALL_TEST_FAIL_AFTER_REPLACE=1 \
  >"$interrupted/output.log" 2>&1; then
  echo "installer succeeded after injected post-replacement failure" >&2
  exit 1
fi
grep -F "printf old" "$interrupted/bin/gate" >/dev/null
grep -F "restored the previous gate binary" "$interrupted/output.log" >/dev/null

locked="$test_root/locked"
mkdir -p "$locked/bin" "$locked/bin/gate.install.lock"
printf '#!/bin/sh\nprintf old\n' >"$locked/bin/gate"
chmod +x "$locked/bin/gate"
printf '%s\n' "$$" >"$locked/bin/gate.install.lock/owner"
if run_installer "$locked" >"$locked/output.log" 2>&1; then
  echo "installer ignored an active destination lock" >&2
  exit 1
fi
grep -F "another gate installation or upgrade" "$locked/output.log" >/dev/null
grep -F "printf old" "$locked/bin/gate" >/dev/null

stale="$test_root/stale"
mkdir -p "$stale/bin/gate.install.lock"
printf '#!/bin/sh\nprintf replacement\n' >"$stale/bin/gate"
printf '#!/bin/sh\nprintf old\n' >"$stale/old-gate"
chmod +x "$stale/bin/gate" "$stale/old-gate"
ln "$stale/old-gate" "$stale/bin/gate.install.lock/previous"
rm -f "$stale/old-gate"
printf '%s\n' "999999" >"$stale/bin/gate.install.lock/owner"
printf '%s\n' "replacing" >"$stale/bin/gate.install.lock/state"
if ! run_installer "$stale" GATE_TEST_GETCAP_STATE=configured >"$stale/output.log" 2>&1; then
  cat "$stale/output.log" >&2
  exit 1
fi
grep -F "recovered the previous gate binary" "$stale/output.log" >/dev/null
test "$(cat "$stale/setup.log")" = "daemon setup --yes"
if rg -F "printf old" "$stale/bin/gate" >/dev/null; then
  echo "stale-lock recovery did not commit the replacement" >&2
  exit 1
fi

concurrent="$test_root/concurrent"
mkdir -p "$concurrent/bin"
printf '#!/bin/sh\nprintf old\n' >"$concurrent/bin/gate"
chmod +x "$concurrent/bin/gate"
run_installer "$concurrent" \
  GATE_TEST_GETCAP_STATE=configured \
  GATE_TEST_SETUP_DELAY=2 \
  >"$concurrent/first.log" 2>&1 &
first_installer_pid=$!
for _ in $(seq 1 100); do
  if [ -d "$concurrent/bin/gate.install.lock" ]; then
    break
  fi
  sleep 0.02
done
if [ ! -d "$concurrent/bin/gate.install.lock" ]; then
  echo "first installer did not acquire the destination lock" >&2
  kill "$first_installer_pid" 2>/dev/null || true
  wait "$first_installer_pid" 2>/dev/null || true
  exit 1
fi
if run_installer "$concurrent" >"$concurrent/second.log" 2>&1; then
  echo "concurrent installer bypassed the destination lock" >&2
  exit 1
fi
grep -F "another gate installation or upgrade" "$concurrent/second.log" >/dev/null
wait "$first_installer_pid"
test "$(cat "$concurrent/setup.log")" = "daemon setup --yes"
if rg -F "printf old" "$concurrent/bin/gate" >/dev/null; then
  echo "concurrent install did not commit the replacement" >&2
  exit 1
fi

if find \
  "$interactive_accept/bin" \
  "$interactive_decline/bin" \
  "$interactive_setup_failure/bin" \
  "$upgrade/bin" \
  "$rollback/bin" \
  "$interrupted/bin" \
  "$stale/bin" \
  "$concurrent/bin" \
  -maxdepth 1 \
  \( -name 'gate.install.lock' -o -name 'gate.install.lock.recover.*' \) -print -quit |
  grep -q .; then
  echo "installer left a transaction artifact behind" >&2
  exit 1
fi
