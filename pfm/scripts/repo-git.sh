#!/usr/bin/env bash
# Sourced by the arch ratchets. repo_git reads the worktree's own index. Inside
# the dev fence a linked worktree's .git names a host path the container cannot
# see, so dev.sh iso hands over the mounted git dir and work tree instead.
# BROKEN STATE: git failing here leaves the caller's file list empty, which
# each caller reports as ERROR "the enumerator did not run" — never PASS.
repo_git() {
  if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" && -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  else
    git "$@"
  fi
}
