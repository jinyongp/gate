# Command runner is `just` (install: https://github.com/casey/just).
set quiet
set positional-arguments

export GOCACHE := "/tmp/gate-gocache"
export GOLANGCI_LINT_CACHE := "/tmp/gate-golangci-cache-gate"

[private]
default:
  @just --list

[doc('build the binary')]
build:
  go run ./cmd/gate-dev build

[doc('run gate from source, e.g. `just gate ls`, `just gate --help`')]
gate *args:
  go run ./cmd/gate-dev run "$@"

# local smoke-test servers
mod smoke 'smoke/.justfile'


[doc('run tests with the race detector')]
test:
  go run ./cmd/gate-dev test

[doc('tests + coverage')]
cover:
  go run ./cmd/gate-dev cover

[doc('check gofmt without writing files')]
fmt-check:
  go run ./cmd/gate-dev fmt-check

[doc('go vet all packages')]
vet:
  go run ./cmd/gate-dev vet

[doc('lint all supported OS targets (text output)')]
lint:
  go run ./cmd/gate-dev lint

[doc('lint for AI/scripts: text diagnostics -> stderr, JSON diagnostics -> stdout')]
lint-json:
  go run ./cmd/gate-dev lint-json

[doc('vulnerability scan (narrowed to actually-called code)')]
vuln:
  go run ./cmd/gate-dev vuln

[doc('shell script syntax/lint smoke checks')]
scripts-check:
  go run ./cmd/gate-dev scripts-check

[doc('test Linux low-port capability and child non-inheritance when supported')]
linux-low-port-test:
  GATE_RUN_LINUX_LOW_PORT_TEST=1 go test ./internal/integrationtest -run '^TestLinuxLowPortIntegration$' -count=1 -v

[doc('check documentation boundaries')]
docs-check:
  go run ./cmd/gate-dev docs-check

[doc('format with gofmt + goimports')]
fmt:
  go run ./cmd/gate-dev fmt

[doc('full gate — run before opening a PR')]
check:
  go run ./cmd/gate-dev check

[doc('release a new version: no arg => interactive patch/minor/major; patch/minor/major -> bump from latest tag; explicit vX.Y.Z')]
release tag="":
  go run ./cmd/gate-dev release {{quote(tag)}}

[doc('cross-compile all release targets into bin/')]
build-all version="dev":
  go run ./cmd/gate-dev build-all {{quote(version)}} bin

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
