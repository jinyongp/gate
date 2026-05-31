#!/usr/bin/env sh
set -eu

VERSION="${PRX_VERSION:-latest}"
SKIP_SKILL_INSTALL="${SKIP_SKILL_INSTALL:-false}"

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

if [ "$VERSION" = "latest" ]; then
  DOWNLOAD_URL="https://github.com/jinyongp/prx/releases/latest/download/prx-${OS}-${ARCH}"
else
  DOWNLOAD_URL="https://github.com/jinyongp/prx/releases/download/${VERSION}/prx-${OS}-${ARCH}"
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required for installation." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

BINARY_PATH="${TMP_DIR}/prx-${OS}-${ARCH}"
if ! curl -fsSL "$DOWNLOAD_URL" -o "$BINARY_PATH"; then
  echo "Failed to download ${DOWNLOAD_URL}" >&2
  echo "Make sure ${VERSION} release exists and includes prx-${OS}-${ARCH}" >&2
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
