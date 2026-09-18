#!/usr/bin/env bash
# Sourced fence environment resolver. Given ROOT (the worktree) and
# FENCE_CALLER (the error prefix), exports the three PFM_DEV_* mount values.
# BROKEN STATE: an unset ROOT prints `fence-env: ROOT unset` and exits 2; a Git
# directory outside the common directory prints the caller-prefixed path error
# and exits 1.
[[ -n "${ROOT:-}" ]] || { echo "fence-env: ROOT unset" >&2; exit 2; }

git_common="$(git -C "$ROOT" rev-parse --git-common-dir)"
[[ "$git_common" == /* ]] || git_common="$ROOT/$git_common"
git_common="$(cd "$git_common" && pwd -P)"
git_dir="$(cd "$(git -C "$ROOT" rev-parse --absolute-git-dir)" && pwd -P)"
case "$git_dir" in
  "$git_common") git_dir_rel="." ;;
  "$git_common"/*) git_dir_rel="${git_dir#"$git_common"/}" ;;
  *) echo "$FENCE_CALLER: git dir $git_dir is outside common dir $git_common" >&2; exit 1 ;;
esac
export PFM_DEV_WORKTREE="$ROOT" PFM_DEV_GIT_COMMON="$git_common" PFM_DEV_GIT_DIR_REL="$git_dir_rel"
