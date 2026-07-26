#!/usr/bin/env bash
set -euo pipefail

mode="${1:-text}"
shift || true

args=(run ./...)

case "$mode" in
  text)
    ;;
  darwin|linux)
    ;;
  json)
    args+=(
      --output.text.path=stderr
      --output.text.colors=false
      --output.json.path=stdout
    )
    ;;
  *)
    echo "usage: scripts/dev/golangci-lint.sh [text|json|darwin|linux]" >&2
    exit 2
    ;;
esac

if [ "$mode" = "darwin" ] || [ "$mode" = "linux" ]; then
  go run -exec "/usr/bin/env GOOS=${mode} GOARCH=amd64" \
    github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 \
    "${args[@]}" "$@"
else
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 \
    "${args[@]}" "$@"
fi
