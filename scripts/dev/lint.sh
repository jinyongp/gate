#!/usr/bin/env bash
set -euo pipefail

scripts/dev/golangci-lint.sh text "$@"

case "$(go env GOHOSTOS)" in
  darwin)
    scripts/dev/golangci-lint.sh linux "$@"
    ;;
  linux)
    scripts/dev/golangci-lint.sh darwin "$@"
    ;;
  *)
    scripts/dev/golangci-lint.sh darwin "$@"
    scripts/dev/golangci-lint.sh linux "$@"
    ;;
esac
