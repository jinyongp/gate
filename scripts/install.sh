#!/usr/bin/env sh
set -eu

VERSION="${GATE_VERSION:-latest}"
REPO="jinyongp/gate"

ui_section() { printf '\n%s\n' "$1"; }
ui_kv() { printf '  %-12s %s\n' "$1" "$2"; }
ui_ok() { printf 'ok: %s\n' "$1"; }
ui_warn_err() { printf 'warning: %s\n' "$1" >&2; }
ui_error() { printf 'error: %s\n' "$1" >&2; }
ui_note() { printf '%s\n' "$1"; }
ui_note_err() { printf '%s\n' "$1" >&2; }
ui_command() { printf '  %s\n' "$1"; }
ui_prompt() { printf '\n%s ' "$1"; }

if [ "$VERSION" != "latest" ] && ! printf '%s\n' "$VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  ui_error "GATE_VERSION must be latest or a stable vMAJOR.MINOR.PATCH tag."
  exit 1
fi

contains_control_char() {
  value="$1"
  cleaned="$(printf '%s' "$value" | LC_ALL=C tr -d '[:cntrl:]')"
  [ "$cleaned" != "$value" ]
}

validate_path_value() {
  name="$1"
  value="$2"
  if contains_control_char "$value"; then
    ui_error "${name} contains control characters."
    exit 1
  fi
}

validate_path_value "HOME" "${HOME:?HOME is required for installation}"
validate_path_value "GATE_BIN_DIR" "${GATE_BIN_DIR:-}"
validate_path_value "SHELL" "${SHELL:-}"

if [ -n "${GATE_BIN_DIR:-}" ]; then
  case "$GATE_BIN_DIR" in
    /*) ;;
    *)
      ui_error "GATE_BIN_DIR must be an absolute path."
      exit 1
      ;;
  esac
  DEST_DIR="${GATE_BIN_DIR}"
else
  DEST_DIR="$HOME/.local/bin"
fi
case "$DEST_DIR" in
  *:*)
    ui_error "install directory contains ':' and cannot be added to PATH safely: ${DEST_DIR}"
    exit 1
    ;;
esac

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"

case "$OS" in
  darwin|linux) ;;
  *)
    ui_error "unsupported OS: $OS"
    ui_note_err "supported OS: darwin, linux"
    exit 1
    ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64)
    ARCH="amd64" ;;
  arm64|aarch64)
    ARCH="arm64" ;;
  *)
    ui_error "unsupported architecture: $ARCH_RAW"
    ui_note_err "supported architecture: amd64, arm64"
    exit 1
    ;;
esac

if ! command -v curl >/dev/null 2>&1; then
  ui_error "curl is required for installation."
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
	if [ -n "${TEMP_DEST:-}" ]; then
		rm -f "$TEMP_DEST"
	fi
	if [ -n "${PREVIOUS_DEST:-}" ]; then
		if [ "${REPLACEMENT_ACTIVE:-0}" -eq 1 ]; then
			if mv -f "$PREVIOUS_DEST" "$DEST"; then
				ui_warn_err "interrupted install restored the previous gate binary."
			else
				ui_error "failed to restore the previous gate binary; recover it from: ${PREVIOUS_DEST}"
				PREVIOUS_DEST=""
			fi
		else
			rm -f "$PREVIOUS_DEST"
		fi
	fi
	if [ "${INSTALL_LOCK_HELD:-0}" -eq 1 ]; then
		rmdir "$INSTALL_LOCK_DIR" 2>/dev/null || true
	fi
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

BINARY_NAME="gate-${OS}-${ARCH}"
BINARY_PATH="${TMP_DIR}/${BINARY_NAME}"
DOWNLOAD_URL=""
CHECKSUMS_URL=""

resolve_download_url() {
  if [ "$VERSION" = "latest" ]; then
    API_URL="https://api.github.com/repos/${REPO}/releases/latest"
  else
    API_URL="https://api.github.com/repos/${REPO}/releases/tags/${VERSION}"
  fi

  RELEASE_JSON="${TMP_DIR}/release.json"
  if ! curl -fsSL -H "Accept: application/vnd.github+json" -H "User-Agent: gate-install" "$API_URL" > "$RELEASE_JSON"; then
    ui_error "failed to read release metadata from GitHub."
    return 1
  fi

  ASSET_URLS="$(tr ',' '\n' < "$RELEASE_JSON" | sed -n 's/.*\"browser_download_url\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p')"

  if [ -z "$ASSET_URLS" ]; then
    return 1
  fi

  CHECKSUMS_URL="$(printf '%s\n' "$ASSET_URLS" | grep '/checksums.txt$' | head -n 1 || true)"

  CANDIDATE="$(printf '%s\n' "$ASSET_URLS" | grep "/${BINARY_NAME}$" | head -n 1 || true)"
  if [ -n "$CANDIDATE" ]; then
    DOWNLOAD_URL="$CANDIDATE"
    return 0
  fi

  return 1
}

verify_checksum() {
  if [ -z "${CHECKSUMS_URL:-}" ]; then
		ui_error "release has no checksums.txt; refusing to install unverified binary."
		return 1
  fi

  CHECKSUMS_FILE="${TMP_DIR}/checksums.txt"
  if ! curl -fsSL "$CHECKSUMS_URL" -o "$CHECKSUMS_FILE"; then
    ui_error "failed to download checksums.txt; refusing to install unverified binary."
    return 1
  fi

  asset_name="$(basename "$DOWNLOAD_URL")"
  expected="$(awk -v f="$asset_name" '$2 == f || $2 == "*"f {print $1; exit}' "$CHECKSUMS_FILE")"
  if [ -z "$expected" ]; then
    ui_error "no checksum entry for ${asset_name}; refusing to install unverified binary."
    return 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$BINARY_PATH" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$BINARY_PATH" | awk '{print $1}')"
  else
    ui_error "no sha256 tool found (sha256sum/shasum); refusing to install unverified binary."
    return 1
  fi

  if [ "$actual" != "$expected" ]; then
    ui_error "checksum verification failed for ${asset_name}."
    ui_note_err "expected: ${expected}"
    ui_note_err "actual:   ${actual}"
    return 1
  fi

  ui_ok "verified checksum for ${asset_name}."
  return 0
}

if ! resolve_download_url; then
  ui_error "no prebuilt release asset found for ${BINARY_NAME}."
  ui_note_err "Publish a GitHub release with ${BINARY_NAME}, or set GATE_VERSION to a release tag that has it."
  exit 1
fi

if ! curl -fsSL "$DOWNLOAD_URL" -o "$BINARY_PATH"; then
  ui_error "failed to download ${BINARY_NAME}."
  exit 1
fi

verify_checksum

if [ ! -f "$BINARY_PATH" ]; then
  ui_error "no installable binary found."
  exit 1
fi
chmod +x "$BINARY_PATH"

if [ -n "${GATE_BIN_DIR:-}" ]; then
  if ! mkdir -p "$DEST_DIR" 2>/dev/null || [ ! -w "$DEST_DIR" ]; then
    ui_error "GATE_BIN_DIR is set but not writable: ${DEST_DIR}"
    exit 1
  fi
elif [ -w "$DEST_DIR" ] || mkdir -p "$DEST_DIR"; then
  :
else
  ui_error "no writable install directory found."
  ui_note_err "Grant permissions or use a custom destination in your shell manually."
  exit 1
fi

DEST="${DEST_DIR}/gate"
if [ -L "$DEST" ] || { [ -e "$DEST" ] && [ ! -f "$DEST" ]; }; then
  ui_error "refusing to replace non-regular install target: ${DEST}"
  exit 1
fi

INSTALL_LOCK_DIR="${DEST}.install.lock"
INSTALL_LOCK_HELD=0
if ! mkdir "$INSTALL_LOCK_DIR" 2>/dev/null; then
  ui_error "another gate installation or upgrade is already using: ${DEST}"
  exit 1
fi
INSTALL_LOCK_HELD=1

find_getcap() {
  if [ -n "${GATE_INSTALL_TEST_GETCAP:-}" ]; then
    case "$GATE_INSTALL_TEST_GETCAP" in
      /*)
        if [ -x "$GATE_INSTALL_TEST_GETCAP" ]; then
          printf '%s\n' "$GATE_INSTALL_TEST_GETCAP"
          return 0
        fi
        ;;
    esac
    return 1
  fi
  for candidate in /usr/sbin/getcap /sbin/getcap /usr/bin/getcap /bin/getcap; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  command -v getcap 2>/dev/null || return 1
}

PREVIOUS_LOW_PORT_CAPABILITY=0
if [ "$OS" = "linux" ] && [ -f "$DEST" ]; then
  GETCAP_BIN="$(find_getcap || true)"
  if [ -z "$GETCAP_BIN" ]; then
    ui_error "getcap is required to safely replace an existing Linux gate binary."
    ui_note_err "Install the libcap tools for your distribution, then retry."
    exit 1
  fi
  if ! PREVIOUS_CAPABILITIES="$("$GETCAP_BIN" "$DEST" 2>/dev/null)"; then
    ui_error "failed to inspect existing Linux capabilities: ${DEST}"
    exit 1
  fi
  PREVIOUS_CAPABILITY_VALUE="$(printf '%s\n' "$PREVIOUS_CAPABILITIES" | awk 'NF { value=$NF } END { print value }')"
  case "$PREVIOUS_CAPABILITY_VALUE" in
    "")
      ;;
    cap_net_bind_service=ep)
      PREVIOUS_LOW_PORT_CAPABILITY=1
      ;;
    *)
      ui_error "existing gate binary has unexpected Linux capabilities: ${PREVIOUS_CAPABILITY_VALUE}"
      ui_note_err "Refusing to replace it automatically."
      exit 1
      ;;
  esac
fi

TEMP_DEST="$(mktemp "${DEST_DIR}/.gate.install.XXXXXX")"
if command -v install >/dev/null 2>&1; then
  install -m 755 "$BINARY_PATH" "$TEMP_DEST"
else
  cp "$BINARY_PATH" "$TEMP_DEST"
  chmod 755 "$TEMP_DEST"
fi

PREVIOUS_DEST=""
REPLACEMENT_ACTIVE=0
if [ -f "$DEST" ]; then
  PREVIOUS_DEST="$(mktemp "${DEST_DIR}/.gate.previous.XXXXXX")"
  rm -f "$PREVIOUS_DEST"
  if ! ln "$DEST" "$PREVIOUS_DEST"; then
    ui_error "failed to preserve existing gate binary before replacement."
    exit 1
  fi
fi
if [ -n "$PREVIOUS_DEST" ]; then
  REPLACEMENT_ACTIVE=1
fi
if ! mv -f "$TEMP_DEST" "$DEST"; then
  ui_error "failed to install gate binary."
  exit 1
fi
TEMP_DEST=""
if [ "${GATE_INSTALL_TEST_FAIL_AFTER_REPLACE:-0}" = "1" ]; then
  ui_error "injected failure after gate replacement."
  exit 97
fi

restore_previous_binary() {
  if [ -z "$PREVIOUS_DEST" ]; then
    return 1
  fi
  if ! mv -f "$PREVIOUS_DEST" "$DEST"; then
    recovery_path="$PREVIOUS_DEST"
    PREVIOUS_DEST=""
    ui_error "failed to restore previous gate binary; recover it from: ${recovery_path}"
    return 1
  fi
  PREVIOUS_DEST=""
  REPLACEMENT_ACTIVE=0
  return 0
}

has_interactive_tty() {
  { [ -t 1 ] || [ -t 2 ]; } && [ -r /dev/tty ] && [ -w /dev/tty ]
}

configure_linux_low_ports() {
  if [ "$OS" != "linux" ]; then
    return 0
  fi

  ui_section "Low-port setup"
  if [ "$PREVIOUS_LOW_PORT_CAPABILITY" -eq 1 ]; then
    ui_note "Preserving permission to bind HTTPS :443 and HTTP :80."
    if "$DEST" daemon setup --yes; then
      ui_ok "preserved Linux low-port capability."
      return 0
    fi
    ui_error "failed to preserve Linux low-port capability on the replacement binary."
    if restore_previous_binary; then
      ui_note_err "Restored the previous gate binary."
    fi
    return 1
  fi

  if ! has_interactive_tty; then
    ui_warn_err "Linux low-port access is not configured in this non-interactive install."
    ui_note_err "Run after installation: gate daemon setup"
    return 0
  fi

  ui_prompt "Allow gate to bind HTTPS :443 and HTTP :80 without running gate as root? [Y/n]:" > /dev/tty
  response=""
  if ! IFS= read -r response < /dev/tty; then
    ui_warn_err "could not read low-port setup choice."
    ui_note_err "Run after installation: gate daemon setup"
    return 0
  fi
  case "$response" in
    n|N|no|No|NO)
      ui_warn_err "Linux low-port setup skipped."
      ui_note_err "Run later: gate daemon setup"
      return 0
      ;;
  esac

  if "$DEST" daemon setup --yes; then
    ui_ok "configured Linux low-port capability."
    return 0
  fi
  ui_warn_err "gate installed, but Linux low-port setup failed."
  ui_note_err "Fix the reported issue, then run: gate daemon setup"
  return 0
}

if ! configure_linux_low_ports; then
  exit 1
fi
REPLACEMENT_ACTIVE=0
if [ -n "$PREVIOUS_DEST" ]; then
  rm -f "$PREVIOUS_DEST"
  PREVIOUS_DEST=""
fi

path_entry_expr() {
	printf '%s\n' "$DEST_DIR"
}

shell_single_quote() {
	printf "'"
	printf '%s' "$1" | sed "s/'/'\\\\''/g"
	printf "'"
}

fish_single_quote() {
	printf "'"
	printf '%s' "$1" | sed "s/\\\\/\\\\\\\\/g; s/'/\\\\'/g"
	printf "'"
}

detected_shell_name() {
  shell_path="${SHELL:-}"
  if [ -z "$shell_path" ]; then
    printf '%s\n' "sh"
    return
  fi
  basename "$shell_path"
}

shell_rc_file() {
  shell_name="$1"
  case "$shell_name" in
    zsh)
      printf '%s\n' "${HOME}/.zshrc"
      ;;
    bash)
      if [ "$OS" = "darwin" ]; then
        if [ -f "${HOME}/.bash_profile" ]; then
          printf '%s\n' "${HOME}/.bash_profile"
        elif [ -f "${HOME}/.bash_login" ]; then
          printf '%s\n' "${HOME}/.bash_login"
        elif [ -f "${HOME}/.profile" ]; then
          printf '%s\n' "${HOME}/.profile"
        else
          printf '%s\n' "${HOME}/.bash_profile"
        fi
      else
        printf '%s\n' "${HOME}/.bashrc"
      fi
      ;;
    fish)
      printf '%s\n' "${HOME}/.config/fish/config.fish"
      ;;
    *)
      printf '%s\n' "${HOME}/.profile"
      ;;
  esac
}

path_update_command() {
  shell_name="$1"
  entry="$(path_entry_expr)"
  case "$shell_name" in
    fish)
			printf 'set -gx PATH %s $PATH\n' "$(fish_single_quote "$entry")"
      ;;
    *)
			printf 'export PATH=%s:"$PATH"\n' "$(shell_single_quote "$entry")"
      ;;
  esac
}

print_path_instructions() {
  shell_name="$1"
  rc_file="$2"
  cmd="$3"
  ui_section "PATH setup"
  ui_note "gate was installed, but ${DEST_DIR} is not in PATH for this terminal."
  if [ -n "$rc_file" ]; then
    ui_note "Add this to ${rc_file}:"
  else
    ui_note "Add this to your shell startup file:"
  fi
  printf '\n'
  ui_command "${cmd}"
  printf '\n'
  ui_note "Then open a new terminal, or run the line above in the current shell."
  if [ "$shell_name" = "fish" ]; then
    ui_note "Detected shell: fish"
  fi
}

append_path_to_rc() {
  rc_file="$1"
  cmd="$2"
  rc_dir="$(dirname "$rc_file")"
  if ! mkdir -p "$rc_dir"; then
    return 1
  fi
  {
    printf '\n# >>> gate PATH >>>\n'
    printf '%s\n' "$cmd"
    printf '# <<< gate PATH <<<\n'
  } >> "$rc_file"
}

configure_path() {
  old_ifs="$IFS"
  IFS=:
  had_noglob=0
  case $- in
    *f*) had_noglob=1 ;;
  esac
  set -f
  path_found=0
  for path_item in ${PATH:-}; do
    if [ "$path_item" = "$DEST_DIR" ]; then
      path_found=1
      break
    fi
  done
  if [ "$had_noglob" -eq 0 ]; then
    set +f
  fi
  IFS="$old_ifs"
  if [ "$path_found" -eq 1 ]; then
    ui_ok "gate is already in your current PATH."
    return
  fi

  shell_name="$(detected_shell_name)"
  rc_file="$(shell_rc_file "$shell_name")"
  entry="$(path_entry_expr)"
  cmd="$(path_update_command "$shell_name")"

  if [ -f "$rc_file" ] && grep -F -x -e "$cmd" "$rc_file" >/dev/null 2>&1; then
    ui_ok "${DEST_DIR} is already listed in ${rc_file}."
    ui_note "Open a new terminal, or run:"
    ui_command "${cmd}"
    return
  fi

  ui_warn_err "PATH does not currently include ${DEST_DIR}."
  if has_interactive_tty; then
    ui_prompt "Add ${DEST_DIR} to PATH in ${rc_file}? [Y/n]:" > /dev/tty
    if IFS= read -r response < /dev/tty; then
      case "$response" in
        ""|y|Y|yes|Yes|YES)
          if append_path_to_rc "$rc_file" "$cmd"; then
            ui_ok "updated ${rc_file}."
            ui_note "Open a new terminal, or run:"
            ui_command "${cmd}"
            return
          fi
          ui_warn_err "could not update ${rc_file}."
          ;;
      esac
    fi
  fi

  print_path_instructions "$shell_name" "$rc_file" "$cmd"
}

ui_section "Install complete"
ui_kv "Binary" "$DEST"

configure_path

resolved="$(command -v gate 2>/dev/null || true)"
if [ -n "$resolved" ] && [ "$resolved" != "$DEST" ]; then
  ui_warn_err "another gate is earlier in PATH and will shadow this install:"
  ui_command "${resolved}"
  ui_note "Remove it, or reorder PATH so ${DEST_DIR} comes first."
fi
