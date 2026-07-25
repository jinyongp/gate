#!/usr/bin/env bash
set -euo pipefail

sh -n scripts/install.sh scripts/uninstall.sh scripts/lib/*.sh scripts/release/build-gate.sh
bash -n .github/scripts/*.sh scripts/dev/*.sh scripts/release/*.sh
bash scripts/dev/test-install-low-ports.sh
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
setup_go_count="$(rg -N 'uses: actions/setup-go@' .github/workflows | wc -l | tr -d ' ')"
go_version_count="$(rg -N -F "go-version: \"${go_minor}.x\"" .github/workflows | wc -l | tr -d ' ')"
check_latest_count="$(rg -N 'check-latest: true' .github/workflows | wc -l | tr -d ' ')"
if [ "$setup_go_count" -eq 0 ] || [ "$setup_go_count" -ne "$go_version_count" ] || [ "$setup_go_count" -ne "$check_latest_count" ]; then
  echo "GitHub Actions must use the current Go minor's latest patch release" >&2
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
if HOME="$script_test_tmp/home" GATE_BIN_DIR=relative/bin sh scripts/uninstall.sh --yes --keep-trust >/dev/null 2>&1; then
  echo "uninstall accepted a relative destination" >&2
  exit 1
fi

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S warning .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh scripts/release/*.sh
fi

if command -v shfmt >/dev/null 2>&1; then
  shfmt -d .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh scripts/release/*.sh
fi
