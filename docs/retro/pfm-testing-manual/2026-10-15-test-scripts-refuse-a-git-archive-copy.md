# 2026-10-15 — `test-pfm.sh` and `test-templates.sh` refuse a `git archive` copy

Observed: engine-sync-tidy phase 3a ran `.claude/scripts/test-pfm.sh` and `test-templates.sh` over a `git archive` copy of the worktree; both exited with "the log directory could not be created". `unit_log` (`.claude/scripts/unit-path.sh`) derives the log directory from `git rev-parse --git-common-dir`, which fails in a copy no repository encloses. Exporting `GIT_DIR=$(git -C {worktree} rev-parse --absolute-git-dir) GIT_WORK_TREE={copy}` first (read-only Git) let both run in the fence.

Amend: .claude/commands/pfm-testing-manual.md#Tricks and traps — the per-unit scripts need a Git repository for their log path; over a `git archive` copy, export `GIT_DIR` (the worktree's absolute gitdir) and `GIT_WORK_TREE` (the copy) first.
