# Gitter Phase Card — SETUP

Gitter phase card — every core `gitter.md` rule (Remote Publication Boundary, Scoped-commit discipline, BANNED commands, commit convention) binds here.

Creates the worktree a flight runs in, before its first task file is dispatched.

## 1. Validate preconditions

- Confirm the brief names the task file(s) (exists on disk) and the residue dir (the flight directory `$HOME/.local/state/pfm/flights/{project}/{flight}/`).
- Confirm no leftover worktree: `./.claude/scripts/worktree.sh list $PIPELINE`. If it exists, warn and stop — never overwrite.
- **Uncommitted changes on main** — handle per the orchestrator's `CarryWIP` directive (`commit` | `leave`, default `leave`). Run only when `git status --porcelain` is non-empty:
  - `commit` — commit main's WIP (untracked included) so the branch inherits it as a shared ancestor:

    ```bash
    git add -A && git commit -m "chore(wip): carry into pipeline/$PIPELINE"
    ```

  - `leave` — leave main's WIP exactly in place, NO stash: `git worktree add` cuts the branch from the COMMITTED ref, so a dirty main never blocks or contaminates worktree creation. A stash+pop window makes live-lane ledgers vanish from sibling readers mid-window, and an aborted phase orphans the stash; a live flight's residue dir is never stashed.
- **Orphan check** — a `pre-pipeline stash: *` entry in `git stash list` is an ABORTED prior SETUP's orphan: stop and report it before any new work (never stack a second; gitter.md § Aborted phase).

## 2. Create worktree

```bash
./.claude/scripts/worktree.sh create $PIPELINE
```

Creates branch `pipeline/$PIPELINE` from `main`, checks out the full repo at `.worktrees/$PIPELINE/`, initializes any submodule projects (`git worktree add` leaves submodule dirs empty; the script runs `git submodule update --init {project}`), installs deps for every roster project, allocates ports, writes `.env.ports`. Then init the audit trail (`$WORKTREE/.checkpoint.json` logs which agent did what; gitignored, archived at MERGE):

```bash
bash .claude/scripts/checkpoint.sh init "$WORKTREE" "$PIPELINE"
```

## 3. Record port assignments

Read `$WORKTREE/.env.ports` and write `ports.md` into the brief-named residue dir from the template in `gitter-history.md` § ports.md Template (`> Author: gitter` byline, per-roster-project + test-infra port table, proxy + port-less notes).

Confirm per template.
