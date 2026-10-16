# 2026-10-15 — `dev.sh iso run` has no Git for a flight worktree

Observed: engine-sync-tidy phase 3b needed Git inside the fence to test a worktree; a linked worktree's `.git` file names a gitdir under the main checkout's `.git/worktrees/`, outside the fence mount, so every `git` call in `dev.sh iso run` failed. The builder extracted `git archive HEAD` on the host into a temporary directory inside the worktree, ran the test against it, and removed it after.

Amend: .claude/commands/pfm-testing-manual.md#Tricks and traps — the fence has no Git for a linked worktree; a test needing a Git tree runs against a `git archive HEAD` copy extracted on the host into a temporary directory inside the worktree, removed after.
