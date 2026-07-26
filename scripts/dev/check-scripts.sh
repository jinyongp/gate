#!/usr/bin/env bash
set -euo pipefail

sh -n scripts/install.sh scripts/uninstall.sh scripts/lib/*.sh scripts/release/build-gate.sh
bash -n .github/scripts/*.sh scripts/dev/*.sh scripts/release/*.sh
bash scripts/dev/test-install-low-ports.sh
bash scripts/dev/test-wait-for-ci.sh
bash scripts/dev/test-check-progress.sh
node scripts/node/check-publish-packages.mjs
if command -v actionlint >/dev/null 2>&1; then
  actionlint
fi

if ! grep -Eq '^toolchain go[0-9]+\.[0-9]+\.[0-9]+$' go.mod; then
  echo "go.mod must pin a patch-level Go toolchain" >&2
  exit 1
fi
go_minor="$(awk '$1 == "go" { print $2; exit }' go.mod)"
if ! [[ "$go_minor" =~ ^[0-9]+\.[0-9]+$ ]]; then
  echo "go.mod must declare a major.minor Go language version" >&2
  exit 1
fi
setup_go_count="$(grep -RhE 'uses: actions/setup-go@' .github/workflows | wc -l | tr -d ' ')"
go_version_count="$(grep -RhF "go-version: \"${go_minor}.x\"" .github/workflows | wc -l | tr -d ' ')"
check_latest_count="$(grep -RhE 'check-latest: true' .github/workflows | wc -l | tr -d ' ')"
if [ "$setup_go_count" -eq 0 ] || [ "$setup_go_count" -ne "$go_version_count" ] || [ "$setup_go_count" -ne "$check_latest_count" ]; then
  echo "GitHub Actions must use the current Go minor's latest patch release" >&2
  exit 1
fi
setup_node_count="$(grep -RhE 'uses: actions/setup-node@' .github/workflows | wc -l | tr -d ' ')"
node_version_file_count="$(grep -RhF 'node-version-file: .node-version' .github/workflows | wc -l | tr -d ' ')"
if [ "$setup_node_count" -eq 0 ] || [ "$setup_node_count" -ne "$node_version_file_count" ]; then
  echo "GitHub Actions must use the repository .node-version" >&2
  exit 1
fi
if ! grep -Fq 'scripts/dev/golangci-lint.sh darwin' scripts/dev/lint.sh ||
  ! grep -Fq 'scripts/dev/golangci-lint.sh linux' scripts/dev/lint.sh; then
  echo "lint must cover every supported OS target from any development host" >&2
  exit 1
fi
if ! grep -Fq 'run: scripts/dev/lint.sh' .github/workflows/ci.yml; then
  echo "CI must use the repository lint command" >&2
  exit 1
fi
if grep -Eq '^  check:' .github/workflows/release.yml ||
  ! grep -Fq 'bash .github/scripts/wait-for-ci.sh "${{ needs.release_tag.outputs.target }}"' \
    .github/workflows/release.yml ||
  ! grep -Fq 'needs: [release_tag, ci_gate]' .github/workflows/release.yml; then
  echo "Release must gate publishing on the exact commit's CI result without rerunning checks" >&2
  exit 1
fi
if ! grep -Fq 'if just check; then' scripts/release/publish.sh ||
  grep -Fq 'out="$(just check' scripts/release/publish.sh; then
  echo "Release checks must stream staged progress from the repository check command" >&2
  exit 1
fi

script_test_tmp="$(mktemp -d)"
trap 'rm -rf "$script_test_tmp"' EXIT
mkdir -p "$script_test_tmp/home" "$script_test_tmp/victim"
if HOME="$script_test_tmp/home" GATE_BIN_DIR="$script_test_tmp/bin:unsafe" sh scripts/install.sh >/dev/null 2>&1; then
  echo "install accepted a PATH-delimited destination" >&2
  exit 1
fi
if HOME="$script_test_tmp/home" GATE_BIN_DIR=relative/bin sh scripts/install.sh >/dev/null 2>&1; then
  echo "install accepted a relative destination" >&2
  exit 1
fi
if HOME="$script_test_tmp/home" GATE_VERSION='../../main' sh scripts/install.sh >/dev/null 2>&1; then
  echo "install accepted an invalid release version" >&2
  exit 1
fi
attack_root="$(printf '%s\n%s' "$script_test_tmp/queued" "$script_test_tmp/victim")"
if HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$attack_root" sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "uninstall accepted a control character in an isolated root" >&2
  exit 1
fi
test -d "$script_test_tmp/victim"
if HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT=/ sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "uninstall accepted filesystem root as isolated root" >&2
  exit 1
fi
mkdir -p "$script_test_tmp/home/run"
printf 'keep\n' > "$script_test_tmp/home/run/victim"
if HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$script_test_tmp/home" sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "uninstall accepted HOME as isolated root" >&2
  exit 1
fi
test -f "$script_test_tmp/home/run/victim"
empty_root="$script_test_tmp/isolated-empty"
mkdir -p "$empty_root/xdg/config/gate"
printf '{"version":1,"records":[]}\n' > "$empty_root/xdg/config/gate/exposures.json"
HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$empty_root" sh scripts/uninstall.sh --yes --keep-trust >/dev/null
test ! -e "$empty_root/xdg/config/gate"
missing_bin_root="$script_test_tmp/isolated-missing-bin"
mkdir -p "$missing_bin_root/xdg/config/gate"
missing_bin_root="$(cd -P "$missing_bin_root" && pwd -P)"
HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$missing_bin_root" \
  GATE_BIN_DIR="$missing_bin_root/missing/bin" \
  sh scripts/uninstall.sh --yes --keep-trust >/dev/null
test ! -e "$missing_bin_root/xdg/config/gate"
if [ "$(uname -s)" = "Darwin" ]; then
  relative_cache_root="$script_test_tmp/isolated-relative-cache"
  mkdir -p "$relative_cache_root/xdg/config/gate"
  HOME="$script_test_tmp/home" XDG_CACHE_HOME=relative/cache \
    GATE_ISOLATED_ROOT="$relative_cache_root" \
    sh scripts/uninstall.sh --yes --keep-trust >/dev/null
  test ! -e "$relative_cache_root/xdg/config/gate"
fi
symlink_root="$script_test_tmp/isolated-symlink"
outside_bin="$script_test_tmp/outside-bin"
mkdir -p "$symlink_root/xdg/config/gate" "$outside_bin"
printf 'outside\n' > "$outside_bin/gate"
ln -s "$outside_bin" "$symlink_root/bin"
if HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$symlink_root" GATE_BIN_DIR="$symlink_root/bin" \
  sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "isolated uninstall accepted a bin directory resolving outside its root" >&2
  exit 1
fi
test "$(cat "$outside_bin/gate")" = "outside"
symlink_state_root="$script_test_tmp/isolated-state-symlink"
outside_state="$script_test_tmp/outside-state"
mkdir -p "$symlink_state_root" "$outside_state/config/gate"
printf 'outside\n' > "$outside_state/config/gate/marker"
ln -s "$outside_state" "$symlink_state_root/xdg"
if HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$symlink_state_root" \
  sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "isolated uninstall followed a state directory symlink outside its root" >&2
  exit 1
fi
test "$(cat "$outside_state/config/gate/marker")" = "outside"
if HOME="$script_test_tmp/home" GATE_BIN_DIR=relative/bin sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "uninstall accepted a relative destination" >&2
  exit 1
fi
standalone_root="$script_test_tmp/standalone-root"
standalone_bin="$standalone_root/bin"
mkdir -p "$standalone_bin" "$standalone_root/xdg/config/gate"
printf 'lock\n' > "$standalone_bin/gate.install.lock"
if [ "$(uname -s)" = "Linux" ]; then
  python3 - "$standalone_bin/gate.install.lock" "$standalone_root/lock-ready" <<'PY' &
import fcntl
import pathlib
import sys
import time

lock = open(sys.argv[1], "a")
fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
pathlib.Path(sys.argv[2]).touch()
time.sleep(30)
PY
  standalone_lock_pid=$!
  for _ in $(seq 1 100); do
    [ -e "$standalone_root/lock-ready" ] && break
    sleep 0.02
  done
  if HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$standalone_root" GATE_BIN_DIR="$standalone_bin" \
    sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
    echo "uninstall ignored an active standalone install lock" >&2
    kill "$standalone_lock_pid" 2>/dev/null || true
    exit 1
  fi
  kill "$standalone_lock_pid" 2>/dev/null || true
  wait "$standalone_lock_pid" 2>/dev/null || true

  overlap_root="$script_test_tmp/cache-overlap"
  mkdir -p "$overlap_root/config/gate" "$overlap_root/bin"
  printf 'keep\n' > "$overlap_root/config/gate/marker"
  ln -s "$overlap_root/config/gate" "$overlap_root/cache-link"
  if HOME="$overlap_root/home" \
    XDG_CONFIG_HOME="$overlap_root/config" \
    XDG_DATA_HOME="$overlap_root/data" \
    XDG_STATE_HOME="$overlap_root/state" \
    XDG_CACHE_HOME="$overlap_root/cache-link/new-cache" \
    GATE_BIN_DIR="$overlap_root/bin" \
    sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
    echo "uninstall accepted a setup lock inside removable state" >&2
    exit 1
  fi
  test "$(cat "$overlap_root/config/gate/marker")" = "keep"
fi
mkdir -p "$standalone_bin/gate.install.transaction"
printf 'state\n' > "$standalone_bin/gate.install.transaction/state"
HOME="$script_test_tmp/home" GATE_ISOLATED_ROOT="$standalone_root" GATE_BIN_DIR="$standalone_bin" \
  sh scripts/uninstall.sh --yes --keep-trust >/dev/null
test -e "$standalone_bin/gate.install.lock"
if [ "$(uname -s)" = "Linux" ]; then
  test ! -e "$standalone_bin/gate.install.transaction"
else
  test -e "$standalone_bin/gate.install.transaction"
fi

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S warning .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh scripts/release/*.sh
fi

if command -v shfmt >/dev/null 2>&1; then
  shfmt -d .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh scripts/release/*.sh
fi
