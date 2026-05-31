#!/usr/bin/env bash
set -euo pipefail

sha256sum prx-darwin-amd64 prx-darwin-arm64 prx-linux-amd64 prx-linux-arm64 > checksums.txt
