# Command runner is `just` (install: https://github.com/casey/just).
set quiet

export GOCACHE := "/tmp/prx-gocache"
export GOLANGCI_LINT_CACHE := "/tmp/prx-golangci-cache"

[private]
default:
  @just --list

[doc('build the binary')]
build:
  scripts/dev/build.sh

[doc('run prx from source, e.g. `just prx ls`, `just prx --help`')]
prx *args:
  scripts/dev/run-prx.sh {{args}}

[doc('run the Go smoke server through the checkout-local prx')]
hello-go:
  just build
  cd examples/hello-go && ../../bin/prx up --daemon && printf '\nopen: https://hello-go.localhost\ncert: browser warning is expected until you trust the local CA\n\n' && ../../bin/prx run web -- go run .

[doc('run the Go smoke server on a random front-proxy port')]
hello-go-port:
  just build
  cd examples/hello-go && ../../bin/prx up --daemon --https-addr :0 --http-addr :0 && printf '\ncert: browser warning is expected until you trust the local CA\n\n' && ../../bin/prx run web -- go run .

[doc('run the Node.js .localhost smoke server through the checkout-local prx')]
hello-js:
  just build
  cd examples/hello-js/localhost && ../../../bin/prx up --daemon && printf '\nopen: https://hello-js.localhost\ncert: browser warning is expected until you trust the local CA\n\n' && ../../../bin/prx run web -- node ../server.mjs

[doc('run the Node.js .localhost smoke server on a random front-proxy port')]
hello-js-port:
  just build
  cd examples/hello-js/localhost && ../../../bin/prx up --daemon --https-addr :0 --http-addr :0 && printf '\ncert: browser warning is expected until you trust the local CA\n\n' && ../../../bin/prx run web -- node ../server.mjs

[doc('run the Node.js custom-domain smoke server through the checkout-local prx')]
hello-js-custom:
  just build
  cd examples/hello-js/custom-domain && ../../../bin/prx daemon stop >/dev/null 2>/dev/null || true
  if command -v lsof >/dev/null 2>/dev/null && lsof -nP -iTCP:443 -sTCP:LISTEN; then printf 'custom-domain smoke requires prx to own :443; stop the process above and retry\n'; exit 4; fi
  if command -v lsof >/dev/null 2>/dev/null && lsof -nP -iTCP:80 -sTCP:LISTEN; then printf 'custom-domain smoke requires prx to own :80; stop the process above and retry\n'; exit 4; fi
  scripts/dev/hello-js-custom-hosts.sh add
  cd examples/hello-js/custom-domain && ../../../bin/prx up --daemon --dns localhost && printf '\nopen: https://hello-js.test\ncert: browser warning is expected until you trust the local CA\ncustom domain uses /etc/hosts for DNS\n\n' && ../../../bin/prx run web -- node ../server.mjs

[doc('remove the hello-js custom-domain hosts entry')]
hello-js-custom-clean:
  cd examples/hello-js/custom-domain && ../../../bin/prx down >/dev/null 2>/dev/null || true
  scripts/dev/hello-js-custom-hosts.sh remove

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

[doc('lint (human-readable)')]
lint:
  scripts/dev/lint.sh

[doc('lint for AI/scripts: human text -> stderr, JSON diagnostics -> stdout')]
lint-json:
  scripts/dev/lint-json.sh

[doc('vulnerability scan (narrowed to actually-called code)')]
vuln:
  scripts/dev/vuln.sh

[doc('shell script syntax/lint smoke checks')]
scripts-check:
  bash scripts/dev/check-scripts.sh

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
  scripts/release/build-prx.sh "{{version}}" bin
