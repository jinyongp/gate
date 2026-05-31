#!/usr/bin/env sh
set -eu

VERSION="${PRX_VERSION:-latest}"
SKIP_SKILL_INSTALL="${SKIP_SKILL_INSTALL:-false}"
REPO="jinyongp/prx"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"

case "$OS" in
  darwin|linux) ;;
  *)
    echo "Unsupported OS: $OS" >&2
    echo "supported OS: darwin, linux" >&2
    exit 1
    ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64)
    ARCH="amd64" ;;
  arm64|aarch64)
    ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH_RAW" >&2
    echo "supported architecture: amd64, arm64" >&2
    exit 1
    ;;
esac

if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required for installation." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

BINARY_NAME="prx-${OS}-${ARCH}"
BINARY_PATH="${TMP_DIR}/${BINARY_NAME}"
DOWNLOAD_URL=""

resolve_download_url() {
  if [ "$VERSION" = "latest" ]; then
    API_URL="https://api.github.com/repos/${REPO}/releases/latest"
    VERSION_LABEL="latest"
  else
    API_URL="https://api.github.com/repos/${REPO}/releases/tags/${VERSION}"
    VERSION_LABEL="$VERSION"
  fi

  RELEASE_JSON="${TMP_DIR}/release.json"
  if ! curl -fsSL -H "Accept: application/vnd.github+json" -H "User-Agent: prx-install" "$API_URL" > "$RELEASE_JSON"; then
    echo "Could not query ${VERSION_LABEL} release from GitHub API." >&2
    return 1
  fi

  ASSET_URLS="$(sed -n 's/.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' "$RELEASE_JSON")"
  TAG_NAME="$(sed -n 's/.*\"tag_name\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p' "$RELEASE_JSON" | head -n 1)"

  if [ -z "$ASSET_URLS" ]; then
    echo "No release assets found for ${VERSION_LABEL} in ${REPO}." >&2
    if [ "$VERSION" = "latest" ]; then
      echo "No release has been published yet." >&2
    fi
    return 1
  fi

  for attempt in "$BINARY_NAME" "${BINARY_NAME}.tar.gz" "${BINARY_NAME}.zip"; do
    CANDIDATE="$(printf '%s\n' "$ASSET_URLS" | grep "/${attempt}$" | head -n 1 || true)"
    if [ -n "$CANDIDATE" ]; then
      DOWNLOAD_URL="$CANDIDATE"
      return 0
    fi
  done

  echo "Could not find ${BINARY_NAME} in release assets for ${VERSION_LABEL}." >&2
  echo "Release tag: ${TAG_NAME}" >&2
  echo "Available assets:" >&2
  printf '%s\n' "$ASSET_URLS" | sed 's#.*/##' >&2
  return 1
}

build_from_source() {
  echo "Falling back to source build." >&2

  if ! command -v git >/dev/null 2>&1; then
    echo "No prebuilt release was found and 'git' is missing." >&2
    echo "Install git, or publish a release with artifacts for ${BINARY_NAME}." >&2
    return 1
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "No prebuilt release was found and 'go' is missing." >&2
    echo "Install Go, or publish a release with artifacts for ${BINARY_NAME}." >&2
    return 1
  fi

  SOURCE_DIR="${TMP_DIR}/source"
  CLONE_URL="https://github.com/${REPO}.git"

  if [ "$VERSION" = "latest" ]; then
    if ! git clone --depth 1 "$CLONE_URL" "$SOURCE_DIR"; then
      echo "Failed to clone ${REPO} (latest)" >&2
      return 1
    fi
  else
    if ! git clone --depth 1 --branch "$VERSION" "$CLONE_URL" "$SOURCE_DIR"; then
      if ! git clone --depth 1 "$CLONE_URL" "$SOURCE_DIR"; then
        echo "Failed to clone ${REPO} for tag ${VERSION}" >&2
        return 1
      fi
      if ! (cd "$SOURCE_DIR" && git checkout "$VERSION"); then
        echo "Release version ${VERSION} not found (tag or branch)." >&2
        return 1
      fi
    fi
  fi

  if ! (cd "$SOURCE_DIR" && go build -trimpath -ldflags "-s -w" -o "$BINARY_PATH" ./cmd/prx); then
    echo "Failed to build prx from source." >&2
    return 1
  fi

  return 0
}

if resolve_download_url; then
  if ! curl -fsSL "$DOWNLOAD_URL" -o "$BINARY_PATH"; then
    echo "Failed to download ${DOWNLOAD_URL}" >&2
    echo "Falling back to source build." >&2
    build_from_source
  fi
else
  build_from_source
fi

if [ ! -f "$BINARY_PATH" ]; then
  echo "No installable binary found."
  exit 1
fi
chmod +x "$BINARY_PATH"

if [ -w /usr/local/bin ]; then
  DEST="/usr/local/bin/prx"
elif [ -w "$HOME/.local/bin" ] || mkdir -p "$HOME/.local/bin"; then
  DEST="$HOME/.local/bin/prx"
else
  echo "Error: no writable install directory found." >&2
  echo "Grant permissions or use a custom destination in your shell manually." >&2
  exit 1
fi

if command -v install >/dev/null 2>&1; then
  install -m 755 "$BINARY_PATH" "$DEST"
else
  cp "$BINARY_PATH" "$DEST"
  chmod 755 "$DEST"
fi

echo "Installed prx to ${DEST}"
if [ "$DEST" = "$HOME/.local/bin/prx" ] && ! printf %s "$PATH" | grep -q "$HOME/.local/bin"; then
  echo "Add it to your PATH:"
  echo "  export PATH=\"$HOME/.local/bin:$PATH\""
fi

if [ "$SKIP_SKILL_INSTALL" = "1" ] || [ "$SKIP_SKILL_INSTALL" = "true" ]; then
  echo "Skipped skill installation (SKIP_SKILL_INSTALL=${SKIP_SKILL_INSTALL})."
  exit 0
fi

echo "Installing agent skill..."

if command -v npx >/dev/null 2>&1; then
  if npx -y skills add jinyongp/prx; then
    echo "Installed prx skill using: npx skills add jinyongp/prx"
    exit 0
  fi
  echo "npx skills add failed." >&2
fi

if command -v apm >/dev/null 2>&1; then
  if apm install jinyongp/prx; then
    echo "Installed prx skill using: apm install jinyongp/prx"
    exit 0
  fi
  echo "apm install failed." >&2
fi

echo "Skill installation failed." >&2
echo "Install a skill manager (npx or apm) and run one of:" >&2
echo "  npx skills add jinyongp/prx" >&2
echo "  apm install jinyongp/prx" >&2
echo "Or skip with SKIP_SKILL_INSTALL=true." >&2
exit 1
