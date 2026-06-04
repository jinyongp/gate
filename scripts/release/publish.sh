#!/usr/bin/env bash
set -euo pipefail

DRY_RUN=0
AUTO_PUSH=0
TAG_INPUT=""
NOTES_BASE_OVERRIDE=""
NOTES_BASE_OVERRIDE_SET=0
CURSOR_HIDDEN=0

SCRIPT_DIR="$(CDPATH="" cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${SCRIPT_DIR}/../lib/ui.sh"

hide_cursor() {
  if [ -t 1 ] && [ "$CURSOR_HIDDEN" -eq 0 ]; then
    printf '\033[?25l'
    CURSOR_HIDDEN=1
  fi
}

show_cursor() {
  if [ "$CURSOR_HIDDEN" -eq 1 ]; then
    printf '\033[?25h'
    CURSOR_HIDDEN=0
  fi
}

abort_interrupted() {
  show_cursor
  printf '\n'
  echo "Aborted."
  exit 130
}

trap abort_interrupted INT
trap show_cursor EXIT

while [ "$#" -gt 0 ]; do
  arg="$1"
  shift

  [ -n "$arg" ] || continue

  case "$arg" in
    --dry-run|-n)
      DRY_RUN=1
      ;;
    --yes|-y)
      AUTO_PUSH=1
      ;;
    tag=*)
      TAG_INPUT="${arg#tag=}"
      ;;
    --since=*)
      NOTES_BASE_OVERRIDE="${arg#--since=}"
      NOTES_BASE_OVERRIDE_SET=1
      ;;
    --since)
      if [ "$#" -eq 0 ]; then
        echo "--since requires a tag"
        exit 1
      fi
      NOTES_BASE_OVERRIDE="$1"
      NOTES_BASE_OVERRIDE_SET=1
      shift
      ;;
    patch|minor|major)
      TAG_INPUT="$arg"
      ;;
    v[0-9]*.[0-9]*.[0-9]*)
      TAG_INPUT="$arg"
      ;;
    *)
      echo "Unknown argument: $arg"
      echo "Usage: scripts/release/publish.sh [--dry-run|-n] [--yes|-y] [--since vX.Y.Z] [patch|minor|major|vX.Y.Z]"
      exit 1
      ;;
  esac
done

get_latest_tag() {
  git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n 1
}

origin_repo_slug() {
  local remote

  remote="$(git remote get-url origin 2>/dev/null || true)"
  case "$remote" in
    https://github.com/*)
      remote="${remote#https://github.com/}"
      ;;
    git@github.com:*)
      remote="${remote#git@github.com:}"
      ;;
    ssh://git@github.com/*)
      remote="${remote#ssh://git@github.com/}"
      ;;
    *)
      return 1
      ;;
  esac
  remote="${remote%.git}"
  if [[ "$remote" =~ ^[^/]+/[^/]+$ ]]; then
    printf '%s\n' "$remote"
    return 0
  fi
  return 1
}

get_latest_published_release_tag() {
  local repo
  local url
  local response
  local error
  local status

  command -v curl >/dev/null 2>&1 || return 1
  repo="$(origin_repo_slug)" || return 1
  url="https://api.github.com/repos/${repo}/releases/latest"
  response="$(mktemp)"
  error="$(mktemp)"
  status=0
  {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      curl -fsSL \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${GITHUB_TOKEN}" \
        "$url"
    else
      curl -fsSL \
        -H "Accept: application/vnd.github+json" \
        "$url"
    fi
  } >"$response" 2>"$error" || status=$?
  if [ "$status" -ne 0 ]; then
    if grep -Eq '(^|[^0-9])404([^0-9]|$)' "$error"; then
      rm -f "$response" "$error"
      return 0
    fi
    cat "$error" >&2
    rm -f "$response" "$error"
    return "$status"
  fi
  sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$response" |
    head -n 1
  rm -f "$response" "$error"
}

sync_tags() {
  if git remote get-url origin >/dev/null 2>&1; then
    git fetch --tags --prune origin
  fi
}

semver_from_tag() {
  local tag="$1"
  local stripped="${tag#v}"

  printf "%s\n" "$stripped" | awk -F. '{print $1" "$2" "$3}'
}

next_version() {
  local major="$1"
  local minor="$2"
  local patch="$3"
  local bump="$4"

  case "$bump" in
    major)
      major=$((major + 1))
      minor=0
      patch=0
      ;;
    minor)
      minor=$((minor + 1))
      patch=0
      ;;
    patch)
      patch=$((patch + 1))
      ;;
    *)
      echo "Unknown bump type: $bump" >&2
      exit 1
      ;;
  esac

  echo "v${major}.${minor}.${patch}"
}

format_commits() {
  local range="$1"

  if [ -z "$range" ]; then
    git log --oneline --no-decorate
  else
    git log --oneline --no-decorate "$range"
  fi
}

commit_range_since() {
  local base="$1"

  if [ -z "$base" ]; then
    echo ""
    return
  fi
  echo "${base}..HEAD"
}

recommended_bump() {
  local range="$1"
  local messages
  local subjects

  messages="$(git log --format='%s%n%b' "$range")"
  subjects="$(git log --format='%s' "$range")"

  if grep -Eq '(^|[[:space:]])BREAKING CHANGE:|^[a-zA-Z]+([(][^)]*[)])?!:' <<<"$messages"; then
    echo "major"
    return
  fi

  if grep -Eq '^feat([(][^)]*[)])?:' <<<"$subjects"; then
    echo "minor"
    return
  fi

  echo "patch"
}

first_match() {
  local input="$1"
  local pattern="$2"

  awk -v pattern="$pattern" '$0 ~ pattern && !found { print; found = 1 }' <<<"$input"
}

recommended_reason() {
  local range="$1"
  local bump="$2"
  local lines
  local messages
  local match

  lines="$(git log --format='%h %s' "$range")"
  messages="$(git log --format='%h %s%n%b' "$range")"

  case "$bump" in
    major)
      match="$(first_match "$lines" '^[^ ]+ [a-zA-Z]+([(][^)]*[)])?!:')"
      if [ -n "$match" ]; then
        echo "$match"
        return
      fi
      match="$(first_match "$messages" 'BREAKING CHANGE:')"
      if [ -n "$match" ]; then
        echo "$match"
        return
      fi
      ;;
    minor)
      match="$(first_match "$lines" '^[^ ]+ feat([(][^)]*[)])?:')"
      if [ -n "$match" ]; then
        echo "$match"
        return
      fi
      ;;
    *)
      first_match "$lines" '.'
      return
      ;;
  esac
}

bump_index() {
  case "$1" in
    patch) echo "0" ;;
    minor) echo "1" ;;
    major) echo "2" ;;
    *) echo "0" ;;
  esac
}

bump_from_index() {
  case "$1" in
    0) echo "patch" ;;
    1) echo "minor" ;;
    2) echo "major" ;;
    *) echo "patch" ;;
  esac
}

candidate_for_bump() {
  case "$1" in
    patch) echo "$PATCH_CANDIDATE" ;;
    minor) echo "$MINOR_CANDIDATE" ;;
    major) echo "$MAJOR_CANDIDATE" ;;
    *) echo "$PATCH_CANDIDATE" ;;
  esac
}

render_bump_option() {
  local index="$1"
  local selected="$2"
  local bump
  local marker="${UI_DIM}○${UI_RESET}"
  local bump_label
  local bump_pad
  local version
  local suffix=""

  bump="$(bump_from_index "$index")"
  bump_label="$bump"
  version="$(candidate_for_bump "$bump")"
  if [ "$index" -eq "$selected" ]; then
    marker="${UI_GREEN}●${UI_RESET}"
    bump_label="${UI_BOLD}${bump}${UI_RESET}"
    version="${UI_CYAN}${version}${UI_RESET}"
  fi
  if [ "$bump" = "$RECOMMENDED_BUMP" ]; then
    suffix="  ${UI_GREEN}recommended${UI_RESET}"
  fi
  printf -v bump_pad '%*s' $((8 - ${#bump})) ''

  printf '  %s  %s%s %s%s\033[K\n' "$marker" "$bump_label" "$bump_pad" "$version" "$suffix"
}

render_bump_menu() {
  local selected="$1"

  render_bump_option 0 "$selected"
  render_bump_option 1 "$selected"
  render_bump_option 2 "$selected"
}

update_bump_option() {
  local index
  local selected
  local up

  index="$1"
  selected="$2"
  up=$((3 - index))

  printf '\033[%dA' "$up"
  render_bump_option "$index" "$selected"
  if [ "$up" -gt 1 ]; then
    printf '\033[%dB' $((up - 1))
  fi
}

select_bump_text() {
  local reply

  while true; do
    ui_prompt "Select bump [patch/minor/major] (default: ${RECOMMENDED_BUMP}):"
    read -r reply

    case "${reply:-$RECOMMENDED_BUMP}" in
      patch|1)
        SELECTED_BUMP="patch"
        return
        ;;
      minor|2)
        SELECTED_BUMP="minor"
        return
        ;;
      major|3)
        SELECTED_BUMP="major"
        return
        ;;
      *)
        echo "Please enter patch, minor, or major."
        ;;
    esac
  done
}

select_bump_radio() {
  local selected
  local previous
  local key
  local rest

  if [ ! -t 0 ] || [ ! -t 1 ] || [ "${TERM:-dumb}" = "dumb" ]; then
    return 1
  fi

  selected="$(bump_index "$RECOMMENDED_BUMP")"
  printf '  Use arrow keys and Enter.\n'
  hide_cursor
  render_bump_menu "$selected"

  while IFS= read -rsn1 key; do
    previous="$selected"

    case "$key" in
      "")
        break
        ;;
      $'\033')
        if IFS= read -rsn2 -t 1 rest; then
          case "$rest" in
            "[A")
              selected=$(((selected + 2) % 3))
              ;;
            "[B")
              selected=$(((selected + 1) % 3))
              ;;
          esac
        fi
        ;;
      j)
        selected=$(((selected + 1) % 3))
        ;;
      k)
        selected=$(((selected + 2) % 3))
        ;;
      1)
        selected=0
        break
        ;;
      2)
        selected=1
        break
        ;;
      3)
        selected=2
        break
        ;;
      p)
        selected=0
        break
        ;;
      m)
        selected=1
        break
        ;;
      M)
        selected=2
        break
        ;;
    esac

    if [ "$selected" -ne "$previous" ]; then
      update_bump_option "$previous" "$selected"
      update_bump_option "$selected" "$selected"
    fi
  done

  SELECTED_BUMP="$(bump_from_index "$selected")"
  show_cursor
}

select_bump() {
  SELECTED_BUMP=""

  if ! select_bump_radio; then
    select_bump_text
  fi
}

# run_checks runs the full gate quietly, collapsing its output to a single
# status line on success and only surfacing the detail when something fails.
run_checks() {
  printf '\nRunning checks (test, lint, vuln)... '
  local out
  if out="$(just check 2>&1)"; then
    ui_ok
  else
    echo "failed"
    printf '\n%s\n\n' "$out"
    echo "Checks failed; aborting release."
    exit 1
  fi
}

confirm_push() {
  local tag="$1"
  local auto="$2"

  if [ "$auto" -eq 1 ]; then
    return 0
  fi

  if [ -n "${CI:-}" ]; then
    return 1
  fi

  ui_prompt "Push branch main and tag ${tag} now? [Y/n]:"
  read -r response
  response="${response:-y}"

  response_lower="$(printf '%s' "$response" | tr '[:upper:]' '[:lower:]')"

  case "$response_lower" in
    y|yes)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

working_tree_changes() {
  git status --porcelain
}

confirm_dirty_tree() {
  local changes="$1"

  ui_section "Uncommitted changes"
  printf '%s\n' "$changes" | sed 's/^/  /'
  echo

  if [ "$DRY_RUN" -eq 1 ]; then
    ui_dim "This is a dry run; no tag or push will be created."
  else
    ui_warn "continuing releases current HEAD only; uncommitted changes are not included."
  fi

  if [ "$AUTO_PUSH" -eq 1 ] || [ -n "${CI:-}" ]; then
    echo "Dirty working tree requires interactive confirmation; aborting release."
    exit 1
  fi

  ui_prompt "Continue with dirty working tree? [y/N]:"
  if ! read -r response; then
    echo
    echo "No response; aborting release."
    exit 1
  fi

  response_lower="$(printf '%s' "$response" | tr '[:upper:]' '[:lower:]')"
  case "$response_lower" in
    y|yes)
      return 0
      ;;
    *)
      echo "Aborted. Commit or stash changes before releasing."
      exit 0
      ;;
  esac
}

DIRTY_CHANGES="$(working_tree_changes)"
if [ -n "$DIRTY_CHANGES" ]; then
  confirm_dirty_tree "$DIRTY_CHANGES"
fi

if ! sync_tags; then
  echo "Failed to fetch tags from origin; aborting to avoid releasing from stale local tags."
  exit 1
fi

LATEST_PUBLISHED_TAG=""
if [ "$NOTES_BASE_OVERRIDE_SET" -eq 1 ]; then
  if [[ ! "$NOTES_BASE_OVERRIDE" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "--since must be a semver tag like v1.2.3"
    exit 1
  fi
  if ! git rev-parse -q --verify "refs/tags/$NOTES_BASE_OVERRIDE" >/dev/null; then
    echo "--since tag does not exist locally: $NOTES_BASE_OVERRIDE"
    exit 1
  fi
  LATEST_PUBLISHED_TAG="$NOTES_BASE_OVERRIDE"
else
  if ! LATEST_PUBLISHED_TAG="$(get_latest_published_release_tag)"; then
    echo "Failed to read latest published GitHub release; use --since vX.Y.Z to set the release notes base explicitly."
    exit 1
  fi
  if [ -n "$LATEST_PUBLISHED_TAG" ] && ! git rev-parse -q --verify "refs/tags/$LATEST_PUBLISHED_TAG" >/dev/null; then
    ui_warn "latest published release tag ${LATEST_PUBLISHED_TAG} is not present locally; release notes will include all commits"
    LATEST_PUBLISHED_TAG=""
  fi
fi
NOTES_RANGE="$(commit_range_since "$LATEST_PUBLISHED_TAG")"

if [ -z "$TAG_INPUT" ]; then
  LATEST_TAG="$(get_latest_tag)"

  if [ -z "$LATEST_TAG" ]; then
    BASE_TAG="v0.0.0"
    LATEST_TAG="$BASE_TAG"
    RANGE=""
    ui_section "Release base"
    printf '  No previous release tag found. First semver tag starts from v0.0.0.\n'
  else
    BASE_TAG="$LATEST_TAG"
    RANGE="${LATEST_TAG}..HEAD"
    ui_section "Release base"
    ui_kv "Last tag" "$LATEST_TAG"
  fi

  ui_section "Version commits since $LATEST_TAG"
  COMMITS="$(format_commits "$RANGE")"
  echo "$COMMITS" | sed 's/^/  - /'
  CHANGE_COUNT="$(printf '%s\n' "$COMMITS" | sed '/^$/d' | wc -l | tr -d ' ')"

  if [ "$CHANGE_COUNT" -eq 0 ]; then
    echo
    echo "No commits to release."
    exit 0
  fi

  read -r BASE_MAJOR BASE_MINOR BASE_PATCH <<<"$(semver_from_tag "$BASE_TAG")"
  PATCH_CANDIDATE="$(next_version "$BASE_MAJOR" "$BASE_MINOR" "$BASE_PATCH" patch)"
  MINOR_CANDIDATE="$(next_version "$BASE_MAJOR" "$BASE_MINOR" "$BASE_PATCH" minor)"
  MAJOR_CANDIDATE="$(next_version "$BASE_MAJOR" "$BASE_MINOR" "$BASE_PATCH" major)"
  RECOMMENDED_BUMP="$(recommended_bump "$RANGE")"
  RECOMMENDED_REASON="$(recommended_reason "$RANGE" "$RECOMMENDED_BUMP")"

  ui_section "Version bump"
  ui_kv "Commits" "$CHANGE_COUNT"
  ui_kv "Recommended" "$RECOMMENDED_BUMP"
  if [ -n "$RECOMMENDED_REASON" ]; then
    ui_kv "Reason" "$RECOMMENDED_REASON"
  fi

  select_bump
  TAG_INPUT="$SELECTED_BUMP"
  PATCH_TAG="$(candidate_for_bump "$TAG_INPUT")"
else
  LATEST_TAG="$(get_latest_tag)"

  if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="v0.0.0"
    RANGE=""
  else
    RANGE="${LATEST_TAG}..HEAD"
  fi

  ui_section "Release base"
  ui_kv "Last tag" "$LATEST_TAG"
  ui_section "Version commits since $LATEST_TAG"
  format_commits "$RANGE" | sed 's/^/  - /'
fi

ui_section "Release notes base"
if [ -n "$LATEST_PUBLISHED_TAG" ]; then
  ui_kv "Last published release" "$LATEST_PUBLISHED_TAG"
  if [ "$LATEST_PUBLISHED_TAG" != "$LATEST_TAG" ]; then
    ui_warn "latest git tag ${LATEST_TAG} has no published release; notes will include commits since ${LATEST_PUBLISHED_TAG}"
  fi
else
  ui_kv "Last published release" "none"
fi
ui_section "Release notes commits"
format_commits "$NOTES_RANGE" | sed 's/^/  - /'

case "$TAG_INPUT" in
  patch|minor|major)
    read -r MAJOR MINOR PATCH <<<"$(semver_from_tag "$LATEST_TAG")"
    PATCH_TAG="$(next_version "$MAJOR" "$MINOR" "$PATCH" "$TAG_INPUT")"
    ;;
  *)
    if [[ ! "$TAG_INPUT" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo "Tag must be vX.Y.Z or one of: patch, minor, major"
      exit 1
    fi

    PATCH_TAG="$TAG_INPUT"
    ;;
esac

if [ "${TAG_INPUT}" != "${PATCH_TAG}" ]; then
  ui_section "Resolved version"
  ui_kv "Tag" "${PATCH_TAG} (from ${TAG_INPUT} bump)"
fi

if [ "$(git symbolic-ref --short HEAD)" != "main" ]; then
  echo "release must run on branch 'main'."
  exit 1
fi

TARGET_SHA="$(git rev-parse HEAD)"

if git rev-parse -q --verify "refs/tags/$PATCH_TAG" >/dev/null; then
  echo "Tag already exists: $PATCH_TAG"
  exit 1
fi

if [ "$DRY_RUN" -eq 0 ] && git ls-remote --exit-code --tags origin "refs/tags/$PATCH_TAG" >/dev/null 2>&1; then
  echo "Remote tag already exists: $PATCH_TAG"
  exit 1
fi

if [ "$DRY_RUN" -eq 1 ]; then
  ui_section "Dry run"
  ui_kv "Tag" "$PATCH_TAG"
  ui_kv "Target" "$TARGET_SHA"
  echo "  No tag or push was created."
  exit 0
fi

run_checks

if ! confirm_push "$PATCH_TAG" "$AUTO_PUSH"; then
  echo "Aborted. No tag created."
  exit 0
fi

RELEASE_NOTES="$(printf 'Release %s\n\n%s' "$PATCH_TAG" "$(format_commits "$NOTES_RANGE" | sed 's/^/- /')")"
git tag -a "$PATCH_TAG" -m "$RELEASE_NOTES" "$TARGET_SHA"
git push --atomic origin HEAD:main "refs/tags/$PATCH_TAG:refs/tags/$PATCH_TAG"
echo "Created and pushed tag $PATCH_TAG"
