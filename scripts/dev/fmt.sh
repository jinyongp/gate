#!/usr/bin/env bash
set -euo pipefail

files=()
while IFS= read -r file; do
	files+=("$file")
done < <(git ls-files --cached --others --exclude-standard '*.go' | grep -v '^internal/truststore/' || true)
if [ "${#files[@]}" -eq 0 ]; then
	exit 0
fi

gofmt -w "${files[@]}"
go run golang.org/x/tools/cmd/goimports@v0.47.0 -w "${files[@]}"
