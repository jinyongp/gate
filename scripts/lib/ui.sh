#!/usr/bin/env sh

ui_env_enabled() {
  case "$1" in
    "" | 0 | false | False | FALSE | no | No | NO | off | Off | OFF)
      return 1
      ;;
    *)
      return 0
      ;;
  esac
}

if [ -n "${NO_COLOR:-}" ]; then
  UI_COLOR=0
elif ui_env_enabled "${FORCE_COLOR:-}" || ui_env_enabled "${CLICOLOR_FORCE:-}"; then
  UI_COLOR=1
elif [ "${CLICOLOR:-}" = "0" ]; then
  UI_COLOR=0
elif [ -t 1 ]; then
  UI_COLOR=1
else
  UI_COLOR=0
fi

if [ "$UI_COLOR" -eq 1 ]; then
  UI_BOLD="$(printf '\033[1m')"
  UI_DIM="$(printf '\033[2m')"
  UI_GREEN="$(printf '\033[32m')"
  UI_RED="$(printf '\033[31m')"
  UI_YELLOW="$(printf '\033[33m')"
  UI_CYAN="$(printf '\033[36m')"
  UI_RESET="$(printf '\033[0m')"
else
  UI_BOLD=""
  UI_DIM=""
  UI_GREEN=""
  UI_RED=""
  UI_YELLOW=""
  UI_CYAN=""
  UI_RESET=""
fi

ui_section() {
  printf '\n%s%s%s\n' "$UI_BOLD" "$1" "$UI_RESET"
}

ui_kv() {
  printf '  %-12s %s\n' "$1" "$2"
}

ui_ok() {
  if [ "$#" -eq 0 ]; then
    printf '%sok%s\n' "$UI_GREEN" "$UI_RESET"
    return
  fi
  printf '%sok:%s %s\n' "$UI_GREEN" "$UI_RESET" "$1"
}

ui_warn() {
  printf '%swarning:%s %s\n' "$UI_YELLOW" "$UI_RESET" "$1"
}

ui_warn_err() {
  ui_warn "$1" >&2
}

ui_error() {
  printf '%serror:%s %s\n' "$UI_RED" "$UI_RESET" "$1" >&2
}

ui_dim() {
  printf '%s%s%s\n' "$UI_DIM" "$1" "$UI_RESET"
}

ui_prompt() {
  printf '\n%s%s%s ' "$UI_BOLD" "$1" "$UI_RESET"
}

export UI_COLOR UI_BOLD UI_DIM UI_GREEN UI_RED UI_YELLOW UI_CYAN UI_RESET
