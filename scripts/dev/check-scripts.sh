#!/usr/bin/env bash
set -euo pipefail

sh -n scripts/install.sh scripts/uninstall.sh scripts/lib/*.sh
bash -n .github/scripts/*.sh scripts/dev/*.sh
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
if ! grep -Fq 'go run ./cmd/gate-dev lint' justfile ||
  ! grep -Fq "run: '\"\$RUNNER_TEMP/gate-dev\" lint'" .github/workflows/ci.yml; then
  echo "Just and CI must use gate-dev lint so every supported OS target is covered" >&2
  exit 1
fi
if ! grep -Fq 'set positional-arguments' justfile ||
  ! grep -Fq 'go run ./cmd/gate-dev run "$@"' justfile; then
  echo "Just must forward gate arguments positionally without shell interpolation" >&2
  exit 1
fi
if ! grep -Fq '"$RUNNER_TEMP/gate-dev" ci build-release-artifacts' .github/workflows/release.yml ||
  ! grep -Fq '"$RUNNER_TEMP/gate-dev" ci checksums' .github/workflows/release.yml ||
  ! grep -Fq '"$RUNNER_TEMP/gate-dev" ci publish-release' .github/workflows/release.yml; then
  echo "Release build and publish jobs must use the job-local gate-dev binary" >&2
  exit 1
fi
if grep -Eq '^  check:' .github/workflows/release.yml ||
  ! grep -Fq '"$RUNNER_TEMP/gate-dev" ci wait-for-ci "${{ needs.release_tag.outputs.target }}"' \
    .github/workflows/release.yml ||
  ! grep -Fq 'needs: [release_tag, ci_gate]' .github/workflows/release.yml; then
  echo "Release must gate publishing on the exact commit's CI result without rerunning checks" >&2
  exit 1
fi
if [ -e scripts/release/publish.sh ] ||
  ! grep -Fq 'go run ./cmd/gate-dev release {{quote(tag)}}' justfile; then
  echo "Local releases must use gate-dev without retaining the legacy publish script" >&2
  exit 1
fi
if ! grep -Fq "GATE_RUN_LINUX_LOW_PORT_TEST=1 go test ./internal/integrationtest" justfile ||
  ! grep -Fq 'GATE_REQUIRE_INSTALL_PTY_TEST: ${{ runner.os == '"'"'Linux'"'"' && '"'"'1'"'"' || '"'"'0'"'"' }}' \
    .github/workflows/ci.yml; then
  echo "Installer and Linux low-port integration must use the Go harness" >&2
  exit 1
fi

for migrated_script in \
  scripts/dev/build.sh \
  scripts/dev/check.sh \
  scripts/dev/cover.sh \
  scripts/dev/docs-check.sh \
  scripts/dev/fmt-check.sh \
  scripts/dev/fmt.sh \
  scripts/dev/go-test-format.sh \
  scripts/dev/golangci-lint.sh \
  scripts/dev/lint-json.sh \
  scripts/dev/lint.sh \
  scripts/dev/run-gate.sh \
  scripts/dev/test-check-progress.sh \
  scripts/dev/test-install-low-ports.sh \
  scripts/dev/test-linux-low-ports.sh \
  scripts/dev/test-wait-for-ci.sh \
  scripts/dev/test.sh \
  scripts/dev/vet.sh \
  scripts/dev/vuln.sh \
  scripts/release/build-gate.sh \
  .github/scripts/build-release-artifacts.sh \
  .github/scripts/checksums.sh \
  .github/scripts/detect-release-tag.sh \
  .github/scripts/generate-homebrew-binary-formula.sh \
  .github/scripts/publish-release.sh \
  .github/scripts/verify-release-tag-target.sh \
  .github/scripts/wait-for-ci.sh \
  .github/scripts/wait-release-assets.sh; do
  if [ -e "$migrated_script" ]; then
    echo "migrated development script still exists: $migrated_script" >&2
    exit 1
  fi
done

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S warning .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh
fi

if command -v shfmt >/dev/null 2>&1; then
  shfmt -d .github/scripts/*.sh scripts/*.sh scripts/dev/*.sh scripts/lib/*.sh
fi
