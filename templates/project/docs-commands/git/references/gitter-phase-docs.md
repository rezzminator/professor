# Gitter Phase Card — DOCS-COMMIT

Gitter phase card — every core `gitter.md` rule (Remote Publication Boundary, Scoped-commit discipline, BANNED commands, commit convention) binds here.

Invoked **after the main-loop session finishes merging permanent docs** (the fix-core card § Step 6) or at wave archive (`/wave:live` W8). The caller passes `Archive:` — pipeline/wave dirs to archive after committing, or `none`.

`{ROSTER_DOC_PATHS}` below is one `{project}/docs/` per roster project. At roster size 1 the repo-root `docs/` already covers it, so the per-project list may be empty.

## 1. Check for doc changes

```bash
git status --short docs/ {ROSTER_DOC_PATHS}
```

If no changes AND `Archive: none`, say "No doc changes to commit" and stop.

## 2. Commit doc changes

The `docs/dev/` pipeline/wave dirs commit here too (git history is their permanent archive). Stage only doc paths:

```bash
git add docs/ {ROSTER_DOC_PATHS}
if ! git diff --cached --quiet; then
  git commit  # type: docs($PIPELINE), desc: "archive pipeline + update docs"
fi
```

**Archive-dir gate (blocking before Step 3):** every `Archive:` dir must be TRACKED AND CLEAN after this commit — `git status --porcelain -- {archive-dir}` returns EMPTY, else add+commit the residue first. Step 3's target `tmp/` is gitignored: a file that reaches the move untracked exits git history forever, so the committed snapshot at the tracked docs path IS the archive.

## 3. Move archived dirs to tmp cold storage

Skip if `Archive: none`. `Archive:` entries may be dirs or single files. Per entry: `docs/dev/builds/*` → `tmp/dev/archive/builds/`, `docs/dev/waves/*` → `tmp/dev/archive/waves/`, `docs/dev/trains/*` → `tmp/dev/archive/trains/`, consumed queue specs `docs/dev/trains/queue/*.md` → `tmp/dev/archive/waves/queue/`. `tmp/` is gitignored — entries stay browseable while git history keeps the committed record; no archive remains under `docs/`.

```bash
mkdir -p tmp/dev/archive/builds tmp/dev/archive/waves tmp/dev/archive/trains tmp/dev/archive/waves/queue
mv {entry} tmp/dev/archive/{builds|waves|trains|waves/queue}/
```

## 4. Commit the removals

On `main` — follow core § Scoped-commit discipline: clear the index, stage only the archive paths (the `-u` deletions come via the explicit dirs), verify, commit, verify the commit.

```bash
git restore --staged .
git add docs/dev/builds/ docs/dev/waves/ docs/dev/trains/
git status --porcelain  # verify ONLY the moved-out dirs are staged
if ! git diff --cached --quiet; then
  git commit  # type: docs($PIPELINE), desc: "move archived pipeline docs to tmp"
  git show --stat HEAD  # verify the commit holds ONLY the intended paths
fi
```

Confirm per template.
