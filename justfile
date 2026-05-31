# Command runner is `just` (install: https://github.com/casey/just).
set quiet

[doc('list recipes')]
default:
  @just --list

[doc('build the binary')]
build:
  go build -trimpath -ldflags "-s -w" -o bin/prx ./cmd/prx

[doc('run tests with the race detector')]
test:
  go test -race ./...

[doc('tests + coverage')]
cover:
  go test -race -cover ./...

[doc('lint (human-readable)')]
lint:
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...

[doc('lint for AI/scripts: human text -> stderr, JSON diagnostics -> stdout')]
lint-json:
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./... --output.text.path=stderr --output.text.colors=false --output.json.path=stdout

[doc('vulnerability scan (narrowed to actually-called code)')]
vuln:
  govulncheck ./...

[doc('format with gofmt + goimports')]
fmt:
  gofmt -w .
  goimports -w .

[doc('full gate — run before opening a PR')]
check: test lint vuln

[doc('release a new version: no arg => interactive patch/minor/major; patch/minor/major -> bump from latest tag; explicit vX.Y.Z')]
release tag="":
  ./scripts/release-publish.sh "{{tag}}"

[doc('cross-compile all release targets into bin/')]
build-all version="dev":
  #!/usr/bin/env bash
  set -euo pipefail
  for t in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
    os="${t%/*}"; arch="${t#*/}"
    GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version={{version}}" -o "bin/prx-$os-$arch" ./cmd/prx
    echo "built bin/prx-$os-$arch"
  done
