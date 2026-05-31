#!/usr/bin/env bash
set -euo pipefail

DRY_RUN=0
AUTO_PUSH=0
TAG_INPUT=""

for arg in "$@"; do
  if [ -z "$arg" ]; then
    continue
  fi

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
    patch|minor|major)
      TAG_INPUT="$arg"
      ;;
    v[0-9]*.[0-9]*.[0-9]*)
      TAG_INPUT="$arg"
      ;;
    *)
      echo "Unknown argument: $arg"
      echo "Usage: scripts/release-publish.sh [--dry-run|-n] [--yes|-y] [patch|minor|major|vX.Y.Z]"
      exit 1
      ;;
  esac
done

INTERACTIVE=1
if [ -n "$TAG_INPUT" ]; then
  INTERACTIVE=0
fi

get_latest_tag() {
  git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n 1
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

confirm_push() {
  local tag="$1"
  local auto="$2"

  if [ "$auto" -eq 1 ]; then
    return 0
  fi

  if [ -n "${CI:-}" ]; then
    return 1
  fi

  printf "Push branch main and tag %s now? [y/N]: " "$tag"
  read -r response

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

if [ -z "$TAG_INPUT" ]; then
  LATEST_TAG="$(get_latest_tag)"

  if [ -z "$LATEST_TAG" ]; then
    BASE_TAG="v0.0.0"
    LATEST_TAG="$BASE_TAG"
    RANGE=""
    echo "No previous release tag found. This will create the first semver tag from v0.0.0."
    echo "Commits since initial commit:"
    COMMITS="$(format_commits "")"
  else
    BASE_TAG="$LATEST_TAG"
    RANGE="${LATEST_TAG}..HEAD"
    echo "Last release tag: $LATEST_TAG"
    echo "Commits since $LATEST_TAG:"
    COMMITS="$(format_commits "$RANGE")"
  fi

  echo "$COMMITS" | sed 's/^/- /'
  CHANGE_COUNT="$(printf "%s" "$COMMITS" | sed '/^$/d' | wc -l | tr -d ' ')"

  if [ "$CHANGE_COUNT" -eq 0 ]; then
    echo "No commits to release."
    exit 0
  fi

  read -r BASE_MAJOR BASE_MINOR BASE_PATCH <<<"$(semver_from_tag "$BASE_TAG")"
  PATCH_CANDIDATE="$(next_version "$BASE_MAJOR" "$BASE_MINOR" "$BASE_PATCH" patch)"
  MINOR_CANDIDATE="$(next_version "$BASE_MAJOR" "$BASE_MINOR" "$BASE_PATCH" minor)"
  MAJOR_CANDIDATE="$(next_version "$BASE_MAJOR" "$BASE_MINOR" "$BASE_PATCH" major)"

  echo
  echo "Commits to include: $CHANGE_COUNT"
  echo "1) patch  -> $PATCH_CANDIDATE"
  echo "2) minor  -> $MINOR_CANDIDATE"
  echo "3) major  -> $MAJOR_CANDIDATE"

  while true; do
    printf "Select bump [1/2/3] (default: 1): "
    read -r REPLY

    case "${REPLY:-1}" in
      1|patch)
        TAG_INPUT=patch
        PATCH_TAG="$PATCH_CANDIDATE"
        break
        ;;
      2|minor)
        TAG_INPUT=minor
        PATCH_TAG="$MINOR_CANDIDATE"
        break
        ;;
      3|major)
        TAG_INPUT=major
        PATCH_TAG="$MAJOR_CANDIDATE"
        break
        ;;
      *)
        echo "Please enter 1, 2, or 3 (or patch/minor/major)."
        ;;
    esac
  done
else
  LATEST_TAG="$(get_latest_tag)"

  if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="v0.0.0"
  fi
fi

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
  echo "Resolved tag: ${PATCH_TAG} (from ${TAG_INPUT} bump)"
fi

if [ "$(git symbolic-ref --short HEAD)" != "main" ]; then
  echo "release must run on branch 'main'."
  exit 1
fi

if [ "$DRY_RUN" -eq 0 ] && [ -n "$(git status --porcelain)" ]; then
  echo "Working tree is dirty. Commit or stash changes first."
  exit 1
fi

if [ "$DRY_RUN" -eq 1 ] && [ -n "$(git status --porcelain)" ]; then
  echo "DRY-RUN: working tree is dirty; continuing without tag checks."
fi

TARGET_SHA="$(git rev-parse HEAD)"

if git rev-parse -q --verify "refs/tags/$PATCH_TAG" >/dev/null; then
  echo "Patch tag already exists: $PATCH_TAG"
  exit 1
fi

if [ "$DRY_RUN" -eq 0 ] && git ls-remote --exit-code --tags origin "refs/tags/$PATCH_TAG" >/dev/null 2>&1; then
  echo "Remote patch tag already exists: $PATCH_TAG"
  exit 1
fi

if [ "$DRY_RUN" -eq 1 ]; then
  echo "DRY-RUN: would create and push tag $PATCH_TAG at $TARGET_SHA"
  exit 0
fi

git tag -a "$PATCH_TAG" -m "Release $PATCH_TAG" "$TARGET_SHA"

if confirm_push "$PATCH_TAG" "$AUTO_PUSH"; then
  git push --follow-tags
  echo "Pushed branch and follow-up tag $PATCH_TAG"
else
  echo "Tag created locally only."
  echo "Push with: git push --follow-tags"
fi
