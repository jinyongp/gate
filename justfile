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

[doc('lint (text output)')]
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
  ./scripts/release/publish.sh "{{tag}}"

[doc('cross-compile all release targets into bin/')]
build-all version="dev":
  scripts/release/build-gate.sh "{{version}}" bin
