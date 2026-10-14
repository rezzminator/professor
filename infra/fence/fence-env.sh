#!/usr/bin/env bash
# Sourced fence environment resolver. Given ROOT (the worktree) and
# FENCE_CALLER (the error prefix), exports the PFM_DEV_* mount values: the three
# Git ones, and the engine-sync source the fence mounts read-only at
# /engine-sync and builds the binary from (infra/fence/engine-sync.sh) —
# $ENGINE_SYNC_SRC when set, else the worktree's own plugins/engine-sync when it
# holds a go.mod (that checkout as it stands, uncommitted edits included, not
# necessarily the commit the tree under test pins), else the main
# checkout's. A
# source without go.mod mounts the common Git directory (already read-only at
# /pfm-git-common) in its place, so the fence still starts and touches nothing
# on the host, and PFM_DEV_ENGINE_SYNC_MISSING names the absence for the build.
# BROKEN STATE: an unset ROOT prints `fence-env: ROOT unset` and exits 2; a Git
# directory outside the common directory, or a relative ENGINE_SYNC_SRC, prints
# the caller-prefixed error and exits 1.
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
engine_sync_src="${ENGINE_SYNC_SRC:-}"
if [[ -z "$engine_sync_src" ]]; then
  if [[ -f "$ROOT/plugins/engine-sync/go.mod" ]]; then
    engine_sync_src="$ROOT/plugins/engine-sync"
  else
    engine_sync_src="$(dirname "$git_common")/plugins/engine-sync"
  fi
fi
[[ "$engine_sync_src" == /* ]] || { echo "$FENCE_CALLER: ENGINE_SYNC_SRC must be an absolute path: $engine_sync_src" >&2; exit 1; }
if [[ -f "$engine_sync_src/go.mod" ]]; then
  export PFM_DEV_ENGINE_SYNC_SRC="$engine_sync_src" PFM_DEV_ENGINE_SYNC_MISSING=""
else
  export PFM_DEV_ENGINE_SYNC_SRC="$git_common" \
    PFM_DEV_ENGINE_SYNC_MISSING="$engine_sync_src: no go.mod (check out plugins/engine-sync, or set ENGINE_SYNC_SRC)"
fi
