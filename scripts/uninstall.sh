#!/usr/bin/env sh
set -eu

FOUND=0
FAILED=0
FORCE=0
WORKFILE="$(mktemp)"
SORTED_FILE="${WORKFILE}.sorted"
trap 'rm -f "$WORKFILE" "$SORTED_FILE"' EXIT

for arg in "$@"; do
  case "$arg" in
    -y|--yes|--force)
      FORCE=1
      ;;
    -h|--help)
      echo "Usage: sh uninstall.sh [--yes|--force|-y]" >&2
      exit 0
      ;;
    *)
      echo "Unsupported argument: $arg" >&2
      exit 1
      ;;
  esac
done

OS="$(uname -s | tr "[:upper:]" "[:lower:]")"
HOME_DIR="${HOME:?HOME is required to locate user uninstall targets}"

prx_config_dir() {
  if [ -n "${XDG_CONFIG_HOME:-}" ]; then
    printf '%s\n' "${XDG_CONFIG_HOME}/prx"
    return
  fi
  printf '%s\n' "${HOME_DIR}/.config/prx"
}

prx_data_dir() {
  if [ -n "${XDG_DATA_HOME:-}" ]; then
    printf '%s\n' "${XDG_DATA_HOME}/prx"
    return
  fi
  printf '%s\n' "${HOME_DIR}/.local/share/prx"
}

prx_state_dir() {
  if [ -n "${XDG_STATE_HOME:-}" ]; then
    printf '%s\n' "${XDG_STATE_HOME}/prx"
    return
  fi
  if [ "$OS" = "darwin" ]; then
    printf '%s\n' "${HOME_DIR}/Library/Logs/prx"
    return
  fi
  printf '%s\n' "${HOME_DIR}/.local/state/prx"
}

collect_paths() {
  cfg_dir="$(prx_config_dir)"
  dat_dir="$(prx_data_dir)"
  st_dir="$(prx_state_dir)"

  if [ -e "$cfg_dir" ] || [ -L "$cfg_dir" ]; then
    printf '%s\n' "$cfg_dir" >> "$WORKFILE"
  fi
  if [ -e "$dat_dir" ] || [ -L "$dat_dir" ]; then
    printf '%s\n' "$dat_dir" >> "$WORKFILE"
  fi
  if [ -e "$st_dir" ] || [ -L "$st_dir" ]; then
    printf '%s\n' "$st_dir" >> "$WORKFILE"
  fi

  if command -v which >/dev/null 2>&1; then
    which -a prx 2>/dev/null | sed '/^$/d' >> "$WORKFILE" || true
  fi
  command -v prx 2>/dev/null | awk '/\// {print $NF}' >> "$WORKFILE" || true

  if [ -n "${PATH:-}" ]; then
    IFS_BACKUP="$IFS"
    IFS=":"
    for dir in $PATH; do
      if [ -n "$dir" ]; then
        if [ -f "$dir/prx" ] || [ -L "$dir/prx" ]; then
          printf "%s\n" "$dir/prx" >> "$WORKFILE"
        fi
      fi
    done
    IFS="$IFS_BACKUP"
  fi
}

collect_paths
sort -u "$WORKFILE" > "$SORTED_FILE"
if [ -s "$SORTED_FILE" ]; then
  printf 'Discovered prx artifacts. Only existing discovered paths will be removed:\n'
  sed 's/^/  - /' "$SORTED_FILE"
  if [ "$FORCE" -ne 1 ]; then
    echo
    printf 'Type y to proceed, anything else to cancel [y/N]: '
    if ! read -r response; then
      echo "Uninstall canceled."
      exit 0
    fi
    case "$response" in
      y|Y|yes|Yes|YES)
        ;;
      *)
        echo "Uninstall canceled."
        exit 0
        ;;
    esac
  fi
fi

stop_daemon() {
  pid_file="$1/prx.pid"
  if [ ! -f "$pid_file" ]; then
    return
  fi
  PID="$(tr -dc '0-9' < "$pid_file" | sed 's/[[:space:]]//g')"
  if [ -z "$PID" ]; then
    return
  fi
  if kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
  fi
}

while IFS= read -r target; do
  if [ ! -e "$target" ] && [ ! -L "$target" ]; then
    continue
  fi

  if [ -d "$target" ]; then
    if [ "$(basename "$target")" = "prx" ]; then
      stop_daemon "$target"
    fi
    if rm -rf "$target"; then status=0; else status=$?; fi
  elif [ -f "$target" ] || [ -L "$target" ]; then
    if rm -f "$target"; then status=0; else status=$?; fi
  else
    status=1
  fi

  if [ "$status" = "0" ]; then
    echo "Removed: $target"
    FOUND=1
  else
    if [ -e "$target" ] || [ -L "$target" ]; then
      echo "Failed to remove: $target"
      FAILED=1
    fi
  fi
done < "$SORTED_FILE"

if [ "$FOUND" -eq 0 ]; then
  echo "No prx installation artifacts found."
  exit 0
fi

if command -v rehash >/dev/null 2>&1; then
  rehash
fi
if command -v hash >/dev/null 2>&1; then
  hash -r
fi

if [ "$FAILED" -eq 1 ]; then
  echo "prx uninstall completed with errors."
  exit 1
fi

echo "prx uninstalled."
