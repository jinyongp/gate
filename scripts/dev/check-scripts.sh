#!/usr/bin/env bash
set -euo pipefail

sh -n scripts/install.sh scripts/uninstall.sh scripts/lib/*.sh scripts/release/build-prx.sh
bash -n .github/scripts/*.sh scripts/dev/*.sh scripts/release/*.sh

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S warning .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh scripts/release/*.sh
fi

if command -v shfmt >/dev/null 2>&1; then
  shfmt -d .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh scripts/release/*.sh
fi
