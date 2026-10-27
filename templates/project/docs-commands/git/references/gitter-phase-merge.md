# Gitter Phase Card — MERGE

Gitter phase card — every core `gitter.md` rule (Remote Publication Boundary, Scoped-commit discipline, BANNED commands, commit convention) binds here. Gitter owns § Gotchas and self-updates it.

## 0. Acquire the merge lock + check for concurrent merges

The advisory lock guards `main` against two pipelines merging at once; busy = another pipeline is mid-merge — report busy, retry shortly. Released in Step 6b. Then check concurrency:

```bash
bash .claude/scripts/git-lock.sh acquire "pipeline/$PIPELINE"
git status --short  # main may carry uncommitted WIP — a flight can launch dirty; expected
ls .worktrees/*/MERGING 2>/dev/null && echo "CONCURRENT MERGE DETECTED" || echo "Clear"
```

If another pipeline is actively merging, wait and retry.

## 1. Validate preconditions

- The REPORT_PATH the brief names must exist ON DISK with every `F{n}` finding `status: resolved @sha`, `resolved in {file:line}` or `waived — {ruling}` — file absent (the merge-gating review never ran) or any `open` finding → refuse and name which. The dispatch brief's own verdict text never substitutes for the file — a merge validated against an in-brief claim is ungated.
- Confirm worktree exists: `./.claude/scripts/worktree.sh list $PIPELINE`

## 2. Commit all worktree changes

```bash
cd $WORKTREE
git add -A
if ! git diff --cached --quiet; then
  git commit  # type: feat($PIPELINE), desc: "$PIPELINE implementation"
fi
cd -
```

## 3. Merge to main

A dirty target checkout never blocks the merge. In the `main` checkout, record `git status --porcelain=v1` and SHA-256 of `git diff`, then merge directly on the dirty tree; `--no-ff` guarantees an explicit merge commit for traceability:

```bash
git merge pipeline/$PIPELINE --no-ff -m "..."  # type: merge($PIPELINE)
```

Git's per-file refusal to overwrite a dirty tracked or untracked path is a STOP naming the overlapping paths, never a stash, reset or clean. After the merge, prove the target's status and diff hash unchanged; a failed read or mismatch is a STOP with the evidence, never a rollback of foreign work. Never stage, commit, stash or discard foreign uncommitted work.

**Branch merge conflicts** — `gitter.md` § Conflicts, then commit: type `merge($PIPELINE)`, desc "resolve conflicts for $PIPELINE".

Verify with `git log --oneline -5`.

## 4. Propagate new .env fields

For each roster project, compare worktree `.env.local`/`.env.test` with main; append new keys (preceded by `# Added by pipeline $PIPELINE`) to main. Skip silently if none.

## 5. Archive the audit trail, then clean up worktree — UNCONDITIONAL

This step runs in the same dispatch as the merge: a source worktree still registered without a keep lock means MERGE is unfinished. The DOCS-COMMIT `Archive:` parameter governs docs archival only — `Archive: none` never skips this step. Salvage before removal: any build doc dirty inside `$WORKTREE/docs/dev/builds/**` diffs against its root counterpart; copy unique or differing content root-side first. Archive the audit trail:

```bash
bash .claude/scripts/checkpoint.sh archive "$WORKTREE" "$DOCS/audit-trail.json"
```

Apply `gitter.md` § Worktree close's KEEP-lock rule first. Otherwise remove the clean worktree and merged branch:

```bash
./.claude/scripts/worktree.sh remove "$PIPELINE" --delete-branch
git worktree list --porcelain  # source registration must be absent
git branch --list "pipeline/$PIPELINE"  # must print nothing
```

The script owns the submodule proof and teardown. A failed removal or branch deletion is unfinished even if the script printed success; report it. Run `gitter.md` § Sweep after cleanup. The completion report ends with § Worktree close's `worktree removed: {path} · branch {deleted|kept}` or `worktree kept (locked): {path} — {reason}` line; no such line means unfinished.

## 6. Update § Gotchas (only if needed)

Update only if a new gotcha, git-structure change, or future-merge workaround was discovered. Never log routine merges.

## 6b. Release the merge lock

```bash
bash .claude/scripts/git-lock.sh release
```

Confirm per template.

## Gotchas

Gitter's living memory of merge gotchas — self-updated when a structural change or recurring problem is discovered, never for routine merges. Seed it from your own repo. Common shapes:

- **Stash pop vs concurrently-written untracked files (SETUP, any `stash push --include-untracked` on main):** untracked pipeline docs are live append-only files another lane's orchestrator may write to _while your stash sits_. Pop can fail with `<file> already exists, no checkout` / `could not restore untracked files from stash` — the stash is kept, not dropped. Never overwrite the newer on-disk file or discard the stash blindly: diff `git show stash@{0}^3:<path>` (untracked blob) against the on-disk version, hand-merge (stashed history + on-disk's newer append, per each file's own append-only convention), write the merged result, verify the full stash is now redundant (`git diff --name-only stash@{0}^1 stash@{0}` for tracked + `git ls-tree -r --name-only stash@{0}^3` for untracked, both checked against `git status --porcelain`), then `git stash drop`.
- **Worktree artifacts:** `.env.ports`, `.env.local`, `.env.test` get staged. Check `git status` and unstage generated files before committing.
- **Dependency-symlink projects:** a roster project whose worktree symlinks the main checkout's dependency dir (e.g. `node_modules`, `.venv`) can appear in `git status` as a new tracked file — unstage before committing. If it slips to main, `git rm --cached {project}/{dep-dir}` and commit immediately.
- **Concurrent pipeline conflicts:** the conflict-awareness check prevents simultaneous merges, not simultaneous development; pipelines that modified the same files conflict at the second merge, and `conflict-resolver` keeps both pipelines' changes.
- **Large-binary / LFS-pointer mismatch blocks merge:** binaries showing `M` on `main` pre-merge despite identical bytes = git expects LFS pointers. `git restore` does NOT fix it; `git stash push -- <files>` (only the flagged binaries) → merge → `git stash pop` does. Long-term: migrate binaries to Git LFS.
- **Test-runner artifact dir blocks merge:** a generated test-output file (e.g. `test-results/.last-run.json`, a coverage report) staged on the pipeline branch fails the merge when the same path already exists untracked on `main`. Fix: `git rm --cached {project}/{artifact-dir}/{file}` in the worktree, ensure `{project}/.gitignore` covers the runner's output dirs, amend the commit, retry.
- **SETUP: a submodule project fails to init until its gitlink commit is pushed:** if a roster project is a git submodule, `git worktree add` + `git submodule update --init {project}` on ANY new pipeline fails with `remote error: upload-pack: not our ref <sha>` whenever main's gitlink OID is ahead of what is actually on the submodule's remote. The object already exists locally (main's own `{project}/` checkout has it, sharing the blob store via `.git/modules/{project}`); the new worktree just gets its own EMPTY per-worktree module dir (`.git/worktrees/$PIPELINE/modules/{project}`) that has to populate that one commit from _somewhere_ local, not the network. Fix, read-only and reversible: `cd .git/worktrees/$PIPELINE/modules/{project} && git fetch {REPO_ROOT}/.git/modules/{project} <sha>` (pulls the object over the local filesystem, no network), then re-run `git -C $WORKTREE submodule update --init {project}` from the worktree root — it now finds the commit locally and checks out clean, with the origin remote untouched. Do NOT `git config submodule.{project}.url <local-path>` as a workaround: without `extensions.worktreeConfig`, `git config` in a worktree writes to the SHARED main `.git/config`, silently repointing main's own submodule url too — unset it immediately if it happens. The gotcha disappears once the gitlink commit is pushed to the submodule's remote.
