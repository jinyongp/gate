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
    "$@" \
    sh scripts/install.sh
}

fresh="$test_root/fresh"
fresh_output="$fresh/output.log"
mkdir -p "$fresh"
run_installer "$fresh" >"$fresh_output" 2>&1
test -x "$fresh/bin/gate"
test ! -e "$fresh/setup.log"
grep -F "gate daemon setup" "$fresh_output" >/dev/null

upgrade="$test_root/upgrade"
mkdir -p "$upgrade/bin"
printf '#!/bin/sh\nprintf old\n' >"$upgrade/bin/gate"
chmod +x "$upgrade/bin/gate"
run_installer "$upgrade" GATE_TEST_GETCAP_STATE=configured >/dev/null 2>&1
grep -F "daemon setup --yes" "$upgrade/setup.log" >/dev/null
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
