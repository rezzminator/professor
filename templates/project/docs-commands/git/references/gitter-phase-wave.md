# Gitter Phase Card — WORKTREE-CHECKPOINT + SYNC

Gitter phase card — every core `gitter.md` rule (Remote Publication Boundary, Scoped-commit discipline, BANNED commands, commit convention) binds here.

## WORKTREE-CHECKPOINT

Invoked by the BUILDER at each task boundary. Commits ON the worktree branch only — no main contact, no merge lock.

1. The brief provides the task id and the EXPLICIT file list (the task's changed files). Stage only that list — a checkpoint that sweeps half-written next-task files corrupts the per-task diff the reviewer later reads.
2. Every git invocation in the phase carries `-C $WORKTREE` from the FIRST command — a bare `cd` + bare `git` sequence is the failure shape (a mis-scoped commit on a dirty main sweeps unrelated files): `git -C $WORKTREE add <explicit paths>` → verify the staged set (`git -C $WORKTREE status --porcelain`) → commit, type `feat($PIPELINE)`, desc `T{n}: {task title}`, standard trailers.
3. Return the checkpoint sha — the builder's ledger-line anchor and the reviewer's diff anchor.

## SYNC

Invoked when a dispatching brief orders it (a long-lived worktree before re-review or the merge). Merges CURRENT `main` INTO the worktree branch so divergence surfaces where the wave's tests can exercise it — never in a blind end-merge.

1. `git -C $WORKTREE merge main --no-edit` (`-C` on every git call — same law as WORKTREE-CHECKPOINT step 2).
2. Conflicts: MAIN wins on files the wave never intentionally changed (a concurrent hotfix on main must survive); the branch wins on the wave's own files; ambiguous overlap on a wave-owned file → report both versions to the dispatching seat and stop, never guess.
3. Report the merged + conflict-resolved file list. The dispatcher re-runs affected test profiles after any conflicted SYNC.
