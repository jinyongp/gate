#!/usr/bin/env bash
set -euo pipefail

scripts/dev/docs-check.sh
scripts/dev/fmt-check.sh
scripts/dev/vet.sh
scripts/dev/cover.sh
scripts/dev/lint.sh
scripts/dev/vuln.sh
scripts/dev/check-scripts.sh
