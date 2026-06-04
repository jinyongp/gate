#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH="" cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -r "${SCRIPT_DIR}/../../scripts/lib/ui.sh" ]; then
  # shellcheck source=../../scripts/lib/ui.sh
  . "${SCRIPT_DIR}/../../scripts/lib/ui.sh"
else
  ui_ok() { printf 'ok: %s\n' "$1"; }
  ui_error() { printf 'error: %s\n' "$1" >&2; }
fi

tap_path="${1:?Usage: generate-homebrew-binary-formula.sh TAP_PATH VERSION_TAG}"
version_tag="${2:?Usage: generate-homebrew-binary-formula.sh TAP_PATH VERSION_TAG}"

case "$version_tag" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    ui_error "version tag must look like vX.Y.Z: ${version_tag}"
    exit 1
    ;;
esac

version="${version_tag#v}"
repo="jinyongp/gate"
release_url="https://github.com/${repo}/releases/download/${version_tag}"
checksums="$(mktemp)"
trap 'rm -f "$checksums"' EXIT

curl -fsSL "${release_url}/checksums.txt" -o "$checksums"

checksum_for() {
  local asset="$1"
  local checksum
  checksum="$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1; found = 1; exit } END { if (found != 1) exit 1 }' "$checksums")"
  if [[ ! "$checksum" =~ ^[0-9a-f]{64}$ ]]; then
    ui_error "invalid or missing checksum for ${asset}"
    exit 1
  fi
  printf '%s\n' "$checksum"
}

darwin_amd64_sha="$(checksum_for gate-darwin-amd64)"
darwin_arm64_sha="$(checksum_for gate-darwin-arm64)"
linux_amd64_sha="$(checksum_for gate-linux-amd64)"
linux_arm64_sha="$(checksum_for gate-linux-arm64)"

formula_dir="${tap_path%/}/Formula"
formula_path="${formula_dir}/gate.rb"
mkdir -p "$formula_dir"

cat > "$formula_path" <<RUBY
class Gate < Formula
  desc "Local-dev global HTTPS reverse proxy and port registry"
  homepage "https://github.com/${repo}"
  version "${version}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "${release_url}/gate-darwin-arm64", using: :nounzip
      sha256 "${darwin_arm64_sha}"
    else
      url "${release_url}/gate-darwin-amd64", using: :nounzip
      sha256 "${darwin_amd64_sha}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "${release_url}/gate-linux-arm64", using: :nounzip
      sha256 "${linux_arm64_sha}"
    else
      url "${release_url}/gate-linux-amd64", using: :nounzip
      sha256 "${linux_amd64_sha}"
    end
  end

  def install
    asset = if OS.mac?
      Hardware::CPU.arm? ? "gate-darwin-arm64" : "gate-darwin-amd64"
    elsif OS.linux?
      Hardware::CPU.arm? ? "gate-linux-arm64" : "gate-linux-amd64"
    else
      odie "unsupported platform"
    end

    chmod 0755, asset
    bin.install asset => "gate"
    generate_completions_from_executable(bin/"gate", "completion")
  end

  def caveats
    <<~EOS
      For full cleanup, run:
        gate uninstall

      \`brew uninstall gate\` removes only the Homebrew package. It does not remove
      gate's local state, trusted root CA, managed hosts block, or shell PATH block.
    EOS
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/gate --version")
  end
end
RUBY

ruby -c "$formula_path" >/dev/null
ui_ok "updated ${formula_path}"
