#!/usr/bin/env sh
set -eu

VERSION="${1:-dev}"
LD_FLAGS="-s -w -X main.version=${VERSION}"

GOOS_LIST="darwin linux"
GOARCH_LIST="arm64 amd64"

for GOOS in $GOOS_LIST; do
  for GOARCH in $GOARCH_LIST; do
    GOOS="$GOOS" GOARCH="$GOARCH" go build \
      -trimpath \
      -ldflags "$LD_FLAGS" \
      -o "prx-${GOOS}-${GOARCH}" \
      ./cmd/prx
  done
done
