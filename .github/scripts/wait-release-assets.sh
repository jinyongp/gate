#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH="" cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -r "${SCRIPT_DIR}/../../scripts/lib/ui.sh" ]; then
  # shellcheck source=../../scripts/lib/ui.sh
  . "${SCRIPT_DIR}/../../scripts/lib/ui.sh"
else
  ui_ok() { printf 'ok: %s\n' "$1"; }
  ui_warn_err() { printf 'warning: %s\n' "$1" >&2; }
  ui_error() { printf 'error: %s\n' "$1" >&2; }
fi

version_tag="${1:?Usage: wait-release-assets.sh VERSION_TAG}"

if [[ ! "$version_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
	ui_error "version tag must look like vX.Y.Z: ${version_tag}"
	exit 1
fi

repo="${GITHUB_REPOSITORY:-jinyongp/gate}"
release_url="https://github.com/${repo}/releases/download/${version_tag}"
max_wait="${GATE_RELEASE_ASSET_WAIT_SECONDS:-600}"
assets=(
  checksums.txt
  gate-darwin-amd64
  gate-darwin-arm64
  gate-linux-amd64
  gate-linux-arm64
)

asset_status() {
  local asset="$1"
  local status
  status="$(curl -sSI --connect-timeout 10 --max-time 30 -o /dev/null -w '%{http_code}' "${release_url}/${asset}" 2>/dev/null || true)"
  if [ -z "$status" ]; then
    status="000"
  fi
  printf '%s\n' "${status: -3}"
}

assets_ready() {
  local asset
  local status
  local ready=0
  for asset in "${assets[@]}"; do
    status="$(asset_status "$asset")"
    case "$status" in
      2?? | 3??) ;;
      *)
        ui_warn_err "release asset not ready (${status}): ${asset}"
        ready=1
        ;;
    esac
  done
  return "$ready"
}

start="$(date +%s)"
attempt=1
delay=1

while ! assets_ready; do
  now="$(date +%s)"
  elapsed=$((now - start))
  if [ "$elapsed" -ge "$max_wait" ]; then
    ui_error "release assets did not become ready within ${max_wait}s"
    exit 1
  fi

  remaining=$((max_wait - elapsed))
  sleep_for="$delay"
  if [ "$sleep_for" -gt "$remaining" ]; then
    sleep_for="$remaining"
  fi
  ui_warn_err "waiting for release assets; retry ${attempt} in ${sleep_for}s"
  sleep "$sleep_for"

  attempt=$((attempt + 1))
  if [ "$delay" -lt 60 ]; then
    delay=$((delay * 2))
    if [ "$delay" -gt 60 ]; then
      delay=60
    fi
  fi
done

ui_ok "release assets are ready"
