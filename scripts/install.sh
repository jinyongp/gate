#!/usr/bin/env sh
set -eu

VERSION="${PRX_VERSION:-latest}"

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

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

BINARY_PATH="${TMP_DIR}/prx-${OS}-${ARCH}"
if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required for installation." >&2
  exit 1
fi

curl -fsSL "$DOWNLOAD_URL" -o "$BINARY_PATH" || {
  echo "Failed to download ${DOWNLOAD_URL}" >&2
  echo "Make sure ${VERSION} release exists and includes prx-${OS}-${ARCH}" >&2
  exit 1
}
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
