# Command runner is `just` (install: https://github.com/casey/just).
set quiet

export GOCACHE := "/tmp/gate-gocache"
export GOLANGCI_LINT_CACHE := "/tmp/gate-golangci-cache-gate"

[private]
default:
  @just --list

[doc('build the binary')]
build:
  scripts/dev/build.sh

[doc('run gate from source, e.g. `just gate ls`, `just gate --help`')]
gate *args:
  scripts/dev/run-gate.sh {{args}}

# local smoke-test servers
mod smoke 'smoke/.justfile'


[doc('run tests with the race detector')]
test:
  scripts/dev/test.sh

[doc('tests + coverage')]
cover:
  scripts/dev/cover.sh

[doc('check gofmt without writing files')]
fmt-check:
  scripts/dev/fmt-check.sh

[doc('go vet all packages')]
vet:
  scripts/dev/vet.sh

[doc('lint all supported OS targets (text output)')]
lint:
  scripts/dev/lint.sh

[doc('lint for AI/scripts: text diagnostics -> stderr, JSON diagnostics -> stdout')]
lint-json:
  scripts/dev/lint-json.sh

[doc('vulnerability scan (narrowed to actually-called code)')]
vuln:
  scripts/dev/vuln.sh

[doc('shell script syntax/lint smoke checks')]
scripts-check:
  bash scripts/dev/check-scripts.sh

[doc('test Linux low-port capability and child non-inheritance when supported')]
linux-low-port-test:
  bash scripts/dev/test-linux-low-ports.sh

[doc('check documentation boundaries')]
docs-check:
  scripts/dev/docs-check.sh

[doc('format with gofmt + goimports')]
fmt:
  scripts/dev/fmt.sh

[doc('full gate — run before opening a PR')]
check:
  scripts/dev/check.sh

[doc('release a new version: no arg => interactive patch/minor/major; patch/minor/major -> bump from latest tag; explicit vX.Y.Z')]
release tag="":
  go run ./cmd/gate-dev release {{quote(tag)}}

[doc('cross-compile all release targets into bin/')]
build-all version="dev":
  scripts/release/build-gate.sh "{{version}}" bin

[doc('build Node API packages')]
node-build:
  pnpm node:build

[doc('typecheck Node API packages')]
node-typecheck:
  pnpm node:typecheck

[doc('dry-run pack Node API JS packages')]
node-pack-dry-run:
  pnpm node:pack:dry-run

[doc('run Node API example smoke tests from packed tarballs')]
node-smoke-examples:
  pnpm node:smoke:examples

[doc('copy release binaries into Node binary package folders')]
node-stage-binaries:
  pnpm node:stage:binaries

[doc('run Node API package tests')]
node-test:
  pnpm node:test

[doc('run Node API package validation')]
node-check:
  pnpm node:check
