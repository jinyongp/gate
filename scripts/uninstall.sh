#!/usr/bin/env sh
set -eu

FOUND=0
FAILED=0
FORCE=0
KEEP_TRUST=0
WORKFILE="$(mktemp)"
SORTED_FILE="${WORKFILE}.sorted"
CLEANUP_FILE="${WORKFILE}.cleanup"
HELPER_FILE="${WORKFILE}.helpers"
INSTALL_LOCKS_FILE="${WORKFILE}.install-locks"
trap 'rm -f "$WORKFILE" "$SORTED_FILE" "$CLEANUP_FILE" "$HELPER_FILE" "$INSTALL_LOCKS_FILE"' EXIT

ui_section() { printf '\n%s\n' "$1"; }
ui_ok() { printf 'ok: %s\n' "$1"; }
ui_error() { printf 'error: %s\n' "$1" >&2; }
ui_prompt() { printf '\n%s ' "$1"; }
ui_subsection() { printf '  %s\n' "$1"; }
ui_item() { printf '  - %s\n' "$1"; }
ui_note() { printf '%s\n' "$1"; }

for arg in "$@"; do
  case "$arg" in
    -y|--yes|--force)
      FORCE=1
      ;;
    --keep-trust)
      KEEP_TRUST=1
      ;;
    -h|--help)
      ui_note "Usage: sh uninstall.sh [--yes|--force|-y] [--keep-trust]" >&2
      exit 0
      ;;
    *)
      ui_error "unsupported argument: $arg"
      exit 1
      ;;
  esac
done

if [ -n "${GATE_BIN_DIR:-}" ]; then
  case "$GATE_BIN_DIR" in
    /*) ;;
    *)
      ui_error "GATE_BIN_DIR must be an absolute path"
      exit 1
      ;;
  esac
fi

OS="$(uname -s | tr "[:upper:]" "[:lower:]")"
HOME_DIR="${HOME:?HOME is required to locate user uninstall targets}"

contains_control_char() {
  value="$1"
  cleaned="$(printf '%s' "$value" | LC_ALL=C tr -d '[:cntrl:]')"
  [ "$cleaned" != "$value" ]
}

for path_value in \
  "$HOME_DIR" \
  "${GATE_ISOLATED_ROOT:-}" \
  "${XDG_CONFIG_HOME:-}" \
  "${XDG_DATA_HOME:-}" \
  "${XDG_STATE_HOME:-}" \
  "${GATE_BIN_DIR:-}" \
  "${GATE_BIN:-}"
do
  if contains_control_char "$path_value"; then
    ui_error "path-bearing environment values must not contain control characters"
    exit 1
  fi
done

if [ "$OS" = "linux" ] && contains_control_char "${XDG_CACHE_HOME:-}"; then
  ui_error "path-bearing environment values must not contain control characters"
  exit 1
fi

if [ "$OS" = "linux" ] && [ -n "${XDG_CACHE_HOME:-}" ]; then
  case "$XDG_CACHE_HOME" in
    /*) ;;
    *)
      ui_error "XDG_CACHE_HOME must be an absolute path"
      exit 1
      ;;
  esac
fi

if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
  case "$GATE_ISOLATED_ROOT" in
    /*) ;;
    *) GATE_ISOLATED_ROOT="$(pwd -P)/${GATE_ISOLATED_ROOT}" ;;
  esac
	if [ -d "$GATE_ISOLATED_ROOT" ]; then
		GATE_ISOLATED_ROOT="$(cd -P "$GATE_ISOLATED_ROOT" && pwd -P)"
	elif [ -L "$GATE_ISOLATED_ROOT" ]; then
		ui_error "isolated root must not be a broken or non-directory symlink"
		exit 1
	fi
	case "$GATE_ISOLATED_ROOT" in
		/)
			ui_error "isolated root is too broad: /"
			exit 1
			;;
	esac
	home_canonical="$(cd -P "$HOME_DIR" && pwd -P)"
	temp_base="${TMPDIR:-/tmp}"
	temp_canonical="$(cd -P "$temp_base" 2>/dev/null && pwd -P || printf '%s\n' "$temp_base")"
	case "$GATE_ISOLATED_ROOT" in
		"$home_canonical"|"$temp_canonical")
			ui_error "isolated root must not be a shared HOME or temporary directory: $GATE_ISOLATED_ROOT"
			exit 1
			;;
	esac
	isolated_relative="${GATE_ISOLATED_ROOT#/}"
	case "$isolated_relative" in
		*/*) ;;
		*)
			ui_error "isolated root must be a dedicated nested directory: $GATE_ISOLATED_ROOT"
			exit 1
			;;
	esac
  if [ -n "${GATE_BIN_DIR:-}" ]; then
    if [ -d "$GATE_BIN_DIR" ]; then
      bin_dir_canonical="$(cd -P "$GATE_BIN_DIR" && pwd -P)"
      case "$bin_dir_canonical" in
        "${GATE_ISOLATED_ROOT}"/*) GATE_BIN_DIR="$bin_dir_canonical" ;;
        *)
          ui_error "isolated GATE_BIN_DIR must resolve inside the isolated root"
          exit 1
          ;;
      esac
    else
      case "$GATE_BIN_DIR" in
        "${GATE_ISOLATED_ROOT}"/*) ;;
        *)
          ui_error "isolated GATE_BIN_DIR must be inside the isolated root"
          exit 1
          ;;
      esac
    fi
  fi
fi

assert_isolated_descendant() {
	path="$1"
	if [ -z "${GATE_ISOLATED_ROOT:-}" ]; then
		return 0
	fi
	case "$path" in
		"${GATE_ISOLATED_ROOT}"/*) return 0 ;;
		*)
			ui_error "refusing unsafe isolated uninstall target: $path"
			return 1
			;;
	esac
}

assert_isolated_removal_target() {
	path="$1"
	if [ -z "${GATE_ISOLATED_ROOT:-}" ]; then
		return 0
	fi
	parent_dir="$(dirname "$path")"
	if ! canonical_parent="$(cd -P "$parent_dir" 2>/dev/null && pwd -P)"; then
		ui_error "refusing unsafe isolated uninstall target: $path"
		return 1
	fi
	case "$canonical_parent" in
		"${GATE_ISOLATED_ROOT}"|"${GATE_ISOLATED_ROOT}"/*) return 0 ;;
		*)
			ui_error "refusing unsafe isolated uninstall target: $path"
			return 1
			;;
	esac
}

gate_config_dir() {
	if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
		printf '%s\n' "${GATE_ISOLATED_ROOT}/xdg/config/gate"
		return
	fi
  if [ -n "${XDG_CONFIG_HOME:-}" ]; then
    printf '%s\n' "${XDG_CONFIG_HOME}/gate"
    return
  fi
  printf '%s\n' "${HOME_DIR}/.config/gate"
}

gate_data_dir() {
	if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
		printf '%s\n' "${GATE_ISOLATED_ROOT}/xdg/data/gate"
		return
	fi
  if [ -n "${XDG_DATA_HOME:-}" ]; then
    printf '%s\n' "${XDG_DATA_HOME}/gate"
    return
  fi
  printf '%s\n' "${HOME_DIR}/.local/share/gate"
}

gate_state_dir() {
	if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
		printf '%s\n' "${GATE_ISOLATED_ROOT}/xdg/state/gate"
		return
	fi
  if [ -n "${XDG_STATE_HOME:-}" ]; then
    printf '%s\n' "${XDG_STATE_HOME}/gate"
    return
  fi
  if [ "$OS" = "darwin" ]; then
    printf '%s\n' "${HOME_DIR}/Library/Logs/gate"
    return
  fi
  printf '%s\n' "${HOME_DIR}/.local/state/gate"
}

gate_runtime_dir() {
  if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
    printf '%s\n' "${GATE_ISOLATED_ROOT}/run"
    return
  fi
  gate_config_dir
}

collect_paths() {
  cfg_dir="$(gate_config_dir)"
  dat_dir="$(gate_data_dir)"
  st_dir="$(gate_state_dir)"
  rt_dir="$(gate_runtime_dir)"
	assert_isolated_descendant "$cfg_dir" || exit 1
	assert_isolated_descendant "$dat_dir" || exit 1
	assert_isolated_descendant "$st_dir" || exit 1
	assert_isolated_descendant "$rt_dir" || exit 1

  if [ -e "$cfg_dir" ] || [ -L "$cfg_dir" ]; then
    printf '%s\n' "$cfg_dir" >> "$WORKFILE"
  fi
  if [ -e "$dat_dir" ] || [ -L "$dat_dir" ]; then
    printf '%s\n' "$dat_dir" >> "$WORKFILE"
  fi
  if [ -e "$st_dir" ] || [ -L "$st_dir" ]; then
    printf '%s\n' "$st_dir" >> "$WORKFILE"
  fi
  if [ -e "$rt_dir" ] || [ -L "$rt_dir" ]; then
    printf '%s\n' "$rt_dir" >> "$WORKFILE"
  fi

  if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
    if [ -n "${GATE_BIN_DIR:-}" ] && [ -d "$GATE_BIN_DIR" ]; then
      append_standalone_install_paths "${GATE_BIN_DIR}/gate"
    fi
  else
    if [ -n "${GATE_BIN_DIR:-}" ] && [ -d "$GATE_BIN_DIR" ]; then
      append_standalone_install_paths "${GATE_BIN_DIR}/gate"
    fi
    append_standalone_install_paths "${HOME_DIR}/.local/bin/gate"
    append_standalone_install_paths "/usr/local/bin/gate"
  fi
}

is_homebrew_gate() {
  path="$1"
  if [ ! -L "$path" ]; then
    return 1
  fi
  target="$(readlink "$path" 2>/dev/null || true)"
  case "$target" in
    */Cellar/gate/*)
      return 0
      ;;
  esac
  return 1
}

append_standalone_install_paths() {
  gate_path="$1"
  if is_homebrew_gate "$gate_path"; then
    return
  fi
  install_paths="$gate_path"
  if [ "$OS" = "linux" ]; then
    install_paths="${install_paths}
${gate_path}.install.transaction"
  fi
  printf '%s\n' "$install_paths" | while IFS= read -r install_path; do
    if [ -e "$install_path" ] || [ -L "$install_path" ]; then
      printf '%s\n' "$install_path" >> "$WORKFILE"
    fi
  done
}

append_cleanup_action() {
  printf '%s\n' "$1" >> "$CLEANUP_FILE"
}

collect_low_port_helpers() {
  : > "$HELPER_FILE"
  if [ "$OS" != "linux" ] || [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
    return
  fi
  if ! command -v find >/dev/null 2>&1; then
    ui_error "find is required to inspect interrupted Linux low-port setup state"
    exit 1
  fi
  helper_uid="$(id -u)"
  for helper_dir in /tmp /var/tmp; do
    [ -d "$helper_dir" ] || continue
    find "$helper_dir" -maxdepth 1 -type f -user 0 \
      \( -perm 0600 -o -perm 0755 \) \
      -name ".gate-capability-helper-${helper_uid}-*" -print >> "$HELPER_FILE"
  done
}

acquire_low_port_setup_lock() {
  if [ "$OS" != "linux" ] || [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
    return
  fi
  find_flock
  if [ -z "$flock_bin" ]; then
    ui_error "flock is required to serialize Linux low-port setup cleanup"
    exit 1
  fi
  capability_cache_dir="${XDG_CACHE_HOME:-${HOME_DIR}/.cache}"
  mkdir -p "$capability_cache_dir"
  capability_cache_dir="$(cd -P "$capability_cache_dir" && pwd -P)"
  capability_lock_dir="${capability_cache_dir}/.gate-capability-locks"
  capability_lock_path="${capability_lock_dir}/capability-setup.lock"
  for removable_gate_dir in \
    "$(gate_config_dir)" \
    "$(gate_data_dir)" \
    "$(gate_state_dir)" \
    "$(gate_runtime_dir)"
  do
    if [ -d "$removable_gate_dir" ]; then
      removable_gate_dir="$(cd -P "$removable_gate_dir" && pwd -P)"
    fi
    case "$capability_lock_dir" in
      "$removable_gate_dir"|"$removable_gate_dir"/*)
        ui_error "low-port setup lock must be outside removable gate state"
        exit 1
        ;;
    esac
  done
  if [ -L "$capability_lock_dir" ] || {
    [ -e "$capability_lock_dir" ] && [ ! -d "$capability_lock_dir" ]
  }; then
    ui_error "refusing unsafe low-port setup lock directory: $capability_lock_dir"
    exit 1
  fi
  mkdir -p "$capability_lock_dir"
  chmod 0700 "$capability_lock_dir"
  if [ -L "$capability_lock_path" ] || {
    [ -e "$capability_lock_path" ] && [ ! -f "$capability_lock_path" ]
  }; then
    ui_error "refusing unsafe low-port setup lock: $capability_lock_path"
    exit 1
  fi
  previous_umask="$(umask)"
  umask 077
  if ! exec 8>>"$capability_lock_path"; then
    umask "$previous_umask"
    ui_error "failed to open low-port setup lock: $capability_lock_path"
    exit 1
  fi
  umask "$previous_umask"
  chmod 0600 "$capability_lock_path"
  if ! find "$capability_lock_path" -maxdepth 0 -type f -user "$(id -u)" \
    -perm 0600 -print | grep -qx "$capability_lock_path"; then
    ui_error "refusing unsafe low-port setup lock: $capability_lock_path"
    exit 1
  fi
  if ! "$flock_bin" -n 8; then
    ui_error "another low-port capability setup is running"
    exit 1
  fi
}

find_flock() {
  flock_bin=""
  for flock_candidate in /usr/bin/flock /bin/flock; do
    if [ -x "$flock_candidate" ]; then
      flock_bin="$flock_candidate"
      return
    fi
  done
  ui_error "flock is required to serialize Linux uninstall"
  exit 1
}

collect_standalone_install_locks() {
  : > "$INSTALL_LOCKS_FILE"
  if [ "$OS" != "linux" ]; then
    return
  fi
  if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
    if [ -n "${GATE_BIN_DIR:-}" ] && [ -d "$GATE_BIN_DIR" ]; then
      case "${GATE_BIN_DIR}/gate" in
        "${GATE_ISOLATED_ROOT}"/*)
          printf '%s\n' "${GATE_BIN_DIR}/gate"
          ;;
      esac
    fi
  else
    if [ -n "${GATE_BIN_DIR:-}" ] && [ -d "$GATE_BIN_DIR" ]; then
      printf '%s\n' "${GATE_BIN_DIR}/gate"
    fi
    if [ -d "${HOME_DIR}/.local/bin" ]; then
      printf '%s\n' "${HOME_DIR}/.local/bin/gate"
    fi
    printf '%s\n' "/usr/local/bin/gate"
  fi |
    while IFS= read -r gate_path; do
      [ -n "$gate_path" ] || continue
      is_homebrew_gate "$gate_path" && continue
      managed_install_path=0
      if [ -n "${GATE_BIN_DIR:-}" ] && [ "$gate_path" = "${GATE_BIN_DIR}/gate" ]; then
        managed_install_path=1
      elif [ -z "${GATE_ISOLATED_ROOT:-}" ] &&
        [ "$gate_path" = "${HOME_DIR}/.local/bin/gate" ]; then
        managed_install_path=1
      fi
      if [ "$managed_install_path" -eq 1 ] ||
        [ -e "$gate_path" ] || [ -L "$gate_path" ] ||
        [ -e "${gate_path}.install.lock" ] || [ -L "${gate_path}.install.lock" ] ||
        [ -e "${gate_path}.install.transaction" ] || [ -L "${gate_path}.install.transaction" ]; then
        printf '%s\n' "${gate_path}.install.lock"
      fi
    done >> "$INSTALL_LOCKS_FILE"
  sort -u "$INSTALL_LOCKS_FILE" -o "$INSTALL_LOCKS_FILE"
}

acquire_standalone_install_locks() {
  if [ ! -s "$INSTALL_LOCKS_FILE" ]; then
    return
  fi
  find_flock
  lock_count=0
  while IFS= read -r install_lock_path; do
    if [ -L "$install_lock_path" ] || {
      [ -e "$install_lock_path" ] && [ ! -f "$install_lock_path" ]
    }; then
      ui_error "refusing unsafe gate install lock: $install_lock_path"
      exit 1
    fi
    previous_umask="$(umask)"
    umask 077
    case "$lock_count" in
      0) exec 7>>"$install_lock_path"; install_lock_fd=7 ;;
      1) exec 6>>"$install_lock_path"; install_lock_fd=6 ;;
      2) exec 5>>"$install_lock_path"; install_lock_fd=5 ;;
      *)
        umask "$previous_umask"
        ui_error "too many standalone gate install locks"
        exit 1
        ;;
    esac
    umask "$previous_umask"
    chmod 0600 "$install_lock_path"
    if ! "$flock_bin" -n "$install_lock_fd"; then
      ui_error "another gate installation or upgrade is using: ${install_lock_path%.install.lock}"
      exit 1
    fi
    lock_count=$((lock_count + 1))
  done < "$INSTALL_LOCKS_FILE"
}

collect_cleanup_actions() {
  dat_dir="$(gate_data_dir)"
  if [ "$KEEP_TRUST" -ne 1 ] && [ -f "${dat_dir}/ca/root.crt" ]; then
    append_cleanup_action "trust store entry for gate root CA"
  fi
  if [ -z "${GATE_ISOLATED_ROOT:-}" ] && [ -f "/etc/hosts" ] && grep -F "# >>> gate managed >>>" "/etc/hosts" >/dev/null 2>&1; then
    append_cleanup_action "managed hosts block in /etc/hosts"
  fi
  if [ -z "${GATE_ISOLATED_ROOT:-}" ]; then
    for rc_file in \
      "${HOME_DIR}/.zshrc" \
      "${HOME_DIR}/.bashrc" \
      "${HOME_DIR}/.bash_profile" \
      "${HOME_DIR}/.bash_login" \
      "${HOME_DIR}/.profile" \
      "${HOME_DIR}/.config/fish/config.fish"
    do
      if grep -F "# >>> gate PATH >>>" "$rc_file" >/dev/null 2>&1; then
        append_cleanup_action "gate PATH block in $rc_file"
      fi
    done
  fi
  collect_low_port_helpers
  if [ -s "$HELPER_FILE" ]; then
    append_cleanup_action "interrupted Linux low-port setup helper"
  fi
}

collect_paths
collect_low_port_helpers
collect_standalone_install_locks
collect_cleanup_actions
sort -u "$WORKFILE" > "$SORTED_FILE"
if [ -s "$SORTED_FILE" ] || [ -s "$CLEANUP_FILE" ]; then
  ui_section "Discovered artifacts"
  if [ -s "$SORTED_FILE" ]; then
    ui_subsection "Existing paths to remove"
    while IFS= read -r target; do
      ui_item "$target"
    done < "$SORTED_FILE"
  fi
  if [ -s "$CLEANUP_FILE" ]; then
    ui_subsection "Cleanup actions"
    while IFS= read -r action; do
      ui_item "$action"
    done < "$CLEANUP_FILE"
  fi
  if [ "$FORCE" -ne 1 ]; then
    ui_prompt "Type y to proceed, anything else to cancel [y/N]:"
    if ! read -r response; then
      ui_note "Uninstall canceled."
      exit 0
    fi
    case "$response" in
      y|Y|yes|Yes|YES)
      ;;
      *)
        ui_note "Uninstall canceled."
        exit 0
        ;;
    esac
  fi
fi

stop_daemon() {
  pid_file="$1"
  if [ ! -f "$pid_file" ]; then
    return
  fi
  PID="$(tr -dc '0-9' < "$pid_file" | sed 's/[[:space:]]//g')"
  if [ -z "$PID" ]; then
    return
  fi
  if kill -0 "$PID" 2>/dev/null; then
    args="$(ps -p "$PID" -o args= 2>/dev/null || true)"
    case "$args" in
      gate\ __serve*|*/gate\ __serve*) ;;
      *)
        ui_error "skipping daemon stop for stale/non-gate pid: $PID"
        return 0
        ;;
    esac
    if ! kill "$PID" 2>/dev/null; then
      ui_error "failed to stop gate daemon: $PID"
      return 1
    fi
    attempts=0
    while kill -0 "$PID" 2>/dev/null && [ "$attempts" -lt 50 ]; do
      sleep 0.1
      attempts=$((attempts + 1))
    done
    if kill -0 "$PID" 2>/dev/null && ! kill -9 "$PID" 2>/dev/null; then
      ui_error "failed to kill gate daemon: $PID"
      return 1
    fi
  fi
}

stop_daemons_in_dir() {
  root="$1"
  stop_daemon "$root/gate.pid" || return 1
  if [ -d "$root/daemons" ]; then
    for pid_file in "$root"/daemons/*.pid; do
      [ -e "$pid_file" ] || continue
      stop_daemon "$pid_file" || return 1
    done
  fi
}

find_gate_binary() {
  if [ -n "${GATE_BIN:-}" ] && [ -x "${GATE_BIN}" ]; then
    printf '%s\n' "${GATE_BIN}"
    return
  fi
  if [ -n "${GATE_BIN_DIR:-}" ] && [ -x "${GATE_BIN_DIR}/gate" ]; then
    printf '%s\n' "${GATE_BIN_DIR}/gate"
    return
  fi
  for candidate in "${HOME_DIR}/.local/bin/gate" "/usr/local/bin/gate"; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return
    fi
  done
  cmd="$(command -v gate 2>/dev/null || true)"
  if [ -n "$cmd" ] && [ -x "$cmd" ]; then
    printf '%s\n' "$cmd"
    return
  fi
}

delegate_exposure_cleanup() {
	exposure_file="$(gate_config_dir)/exposures.json"
	if [ ! -e "$exposure_file" ]; then
		return
	fi
	exposure_compact="$(tr -d '[:space:]' < "$exposure_file")"
	case "$exposure_compact" in
		*'"records":null'*|*'"records":[]'*) return ;;
		*'"records":['*) ;;
		*)
			ui_error "exposure state is malformed; restore gate and run gate uninstall"
			exit 1
			;;
	esac
	gate_bin="$(find_gate_binary || true)"
	if [ -z "${gate_bin:-}" ]; then
		ui_error "active exposure state exists but the gate binary is unavailable; restore gate and run gate uninstall"
		exit 1
	fi
	if [ "$KEEP_TRUST" -eq 1 ]; then
		exec "$gate_bin" uninstall --yes --keep-brew --keep-trust
	fi
	exec "$gate_bin" uninstall --yes --keep-brew
}

untrust_gate() {
  if [ "$KEEP_TRUST" -eq 1 ]; then
    return
  fi
  dat_dir="$(gate_data_dir)"
  if [ ! -f "${dat_dir}/ca/root.crt" ]; then
    return
  fi
  gate_bin="$(find_gate_binary || true)"
  if [ -z "${gate_bin:-}" ]; then
    ui_error "skipping trust cleanup: gate binary not found"
    FAILED=1
    return
  fi
  ui_section "Trust store cleanup"
  if "$gate_bin" untrust; then
    ui_ok "removed trusted gate root CA"
    FOUND=1
    return
  fi
  ui_error "failed to remove trusted gate root CA"
  FAILED=1
}

delegate_exposure_cleanup
collect_low_port_helpers
collect_standalone_install_locks
if [ -s "$WORKFILE" ] || [ -s "$HELPER_FILE" ] || [ -s "$CLEANUP_FILE" ] ||
  command -v gate >/dev/null 2>&1; then
  acquire_low_port_setup_lock
  acquire_standalone_install_locks
  collect_low_port_helpers
fi
untrust_gate
if [ "$FAILED" -eq 1 ]; then
	ui_error "gate uninstall stopped before deleting files because trust cleanup failed."
	exit 1
fi
if ! stop_daemons_in_dir "$(gate_config_dir)" || ! stop_daemons_in_dir "$(gate_runtime_dir)"; then
	ui_error "gate uninstall stopped before deleting files because a daemon could not be stopped."
	exit 1
fi

remove_marked_block() {
  path="$1"
  begin="$2"
  end="$3"
  if [ ! -f "$path" ]; then
    return 0
  fi
	if [ -L "$path" ]; then
		ui_error "refusing to rewrite symlinked shell startup file atomically: $path"
		return 1
	fi
  if ! grep -F "$begin" "$path" >/dev/null 2>&1; then
    return 0
  fi
	dir="${path%/*}"
	base="${path##*/}"
	[ "$dir" != "$path" ] || dir="."
	tmp="$(mktemp "${dir}/.${base}.gate.XXXXXX")" || return 1
  awk -v begin="$begin" -v end="$end" '
    $0 == begin { skip = 1; changed = 1; next }
    $0 == end && skip == 1 { skip = 0; ended = 1; next }
    skip != 1 { print }
    END {
      if (changed != 1) exit 3
      if (ended != 1) exit 2
    }
  ' "$path" > "$tmp" || {
    status=$?
    rm -f "$tmp"
    return "$status"
  }
  if cmp -s "$path" "$tmp"; then
    rm -f "$tmp"
    return 0
  fi
	mode=""
	if mode="$(stat -f '%Lp' "$path" 2>/dev/null)"; then
		:
	elif mode="$(stat -c '%a' "$path" 2>/dev/null)"; then
		:
	fi
	if [ -n "$mode" ] && ! chmod "$mode" "$tmp"; then
		rm -f -- "$tmp"
		return 1
	fi
	if mv -f -- "$tmp" "$path"; then
		return 0
	fi
	rm -f -- "$tmp"
	return 1
}

cleanup_path_blocks() {
  for rc_file in \
    "${HOME_DIR}/.zshrc" \
    "${HOME_DIR}/.bashrc" \
    "${HOME_DIR}/.bash_profile" \
    "${HOME_DIR}/.bash_login" \
    "${HOME_DIR}/.profile" \
    "${HOME_DIR}/.config/fish/config.fish"
  do
    if grep -F "# >>> gate PATH >>>" "$rc_file" >/dev/null 2>&1; then
      if remove_marked_block "$rc_file" "# >>> gate PATH >>>" "# <<< gate PATH <<<"; then
        ui_ok "removed gate PATH block from $rc_file"
        FOUND=1
      else
        ui_error "failed to remove gate PATH block from: $rc_file"
        FAILED=1
      fi
    fi
  done
}

cleanup_hosts_block() {
  if [ -n "${GATE_ISOLATED_ROOT:-}" ]; then
    return
  fi
  hosts_file="/etc/hosts"
  if [ ! -f "$hosts_file" ]; then
    return
  fi
  if [ -L "$hosts_file" ]; then
    ui_error "skipping hosts cleanup: /etc/hosts is a symlink"
    FAILED=1
    return
  fi
  if ! grep -F "# >>> gate managed >>>" "$hosts_file" >/dev/null 2>&1; then
    return
  fi
  tmp="$(mktemp)"
  awk '
    $0 == "# >>> gate managed >>>" { skip = 1; changed = 1; next }
    $0 == "# <<< gate managed <<<" && skip == 1 { skip = 0; ended = 1; next }
    skip != 1 { print }
    END {
      if (changed != 1) exit 3
      if (ended != 1) exit 2
    }
  ' "$hosts_file" > "$tmp" || {
    status=$?
    rm -f "$tmp"
    ui_error "failed to prepare hosts cleanup"
    FAILED=1
    return
  }
  if cmp -s "$hosts_file" "$tmp"; then
    rm -f "$tmp"
    return
  fi
  dst="${hosts_file}.gate.tmp.$$"
  if run_privileged install -m 0644 "$tmp" "$dst" && run_privileged mv "$dst" "$hosts_file"; then
    ui_ok "removed gate block from /etc/hosts"
    FOUND=1
  else
    ui_error "failed to remove gate block from /etc/hosts"
    FAILED=1
    run_privileged rm -f "$dst" 2>/dev/null || true
  fi
  rm -f "$tmp"
}

run_privileged() {
  if command -v sudo >/dev/null 2>&1; then
    sudo "$@"
    return
  fi
  "$@"
}

cleanup_low_port_helpers() {
  collect_low_port_helpers
  if [ ! -s "$HELPER_FILE" ]; then
    return
  fi
  while IFS= read -r helper_path; do
    case "$helper_path" in
      /tmp/.gate-capability-helper-*|/var/tmp/.gate-capability-helper-*) ;;
      *)
        ui_error "refusing unsafe low-port helper path: $helper_path"
        FAILED=1
        continue
        ;;
    esac
    if run_privileged rm -f -- "$helper_path"; then
      ui_ok "removed interrupted Linux low-port setup helper"
      FOUND=1
    else
      ui_error "failed to remove interrupted Linux low-port setup helper: $helper_path"
      FAILED=1
    fi
  done < "$HELPER_FILE"
}

if [ -z "${GATE_ISOLATED_ROOT:-}" ]; then
  cleanup_path_blocks
  cleanup_hosts_block
fi
cleanup_low_port_helpers

while IFS= read -r target; do
  if [ ! -e "$target" ] && [ ! -L "$target" ]; then
    continue
  fi
  if ! assert_isolated_removal_target "$target"; then
    FAILED=1
    continue
  fi

  if [ -d "$target" ]; then
    if rm -rf -- "$target"; then status=0; else status=$?; fi
  elif [ -f "$target" ] || [ -L "$target" ]; then
    if rm -f -- "$target"; then status=0; else status=$?; fi
  else
    status=1
  fi

  if [ "$status" = "0" ]; then
    ui_ok "removed $target"
    FOUND=1
  else
    if [ -e "$target" ] || [ -L "$target" ]; then
      ui_error "failed to remove: $target"
      FAILED=1
    fi
  fi
done < "$SORTED_FILE"

if command -v rehash >/dev/null 2>&1; then
  rehash
fi
if command -v hash >/dev/null 2>&1; then
  hash -r
fi

if [ "$FAILED" -eq 1 ]; then
  ui_error "gate uninstall completed with errors."
  exit 1
fi

if [ "$FOUND" -eq 0 ]; then
  ui_section "Uninstall complete"
  ui_note "No gate installation artifacts found."
  exit 0
fi

ui_section "Uninstall complete"
ui_ok "gate uninstalled."
