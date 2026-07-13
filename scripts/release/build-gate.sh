#!/usr/bin/env sh
set -eu

VERSION="${1:-dev}"
OUT_DIR="${2:-.}"
LD_FLAGS="-s -w -X main.version=${VERSION}"

GOOS_LIST="darwin linux"
GOARCH_LIST="arm64 amd64"

mkdir -p "$OUT_DIR"

for target_os in $GOOS_LIST; do
  for target_arch in $GOARCH_LIST; do
    out="${OUT_DIR}/gate-${target_os}-${target_arch}"
		CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
      -trimpath \
      -ldflags "$LD_FLAGS" \
      -o "$out" \
      ./cmd/gate
    echo "built $out"
  done
done
