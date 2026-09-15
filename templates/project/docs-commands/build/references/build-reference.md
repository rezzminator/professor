# /wave:builder Reference

Detailed mechanics the `/wave:builder` orchestrator reads on demand: the Step 0a stale-cleanup procedure, the BLOCKED.md template, and the full pipeline step map.

## Contents

- § 0a — Stale pipeline cleanup
- § Audit trail — per-phase checkpoint logging
- § BLOCKED.md — Fix Loop Escalation template
- § Pipeline step map — what each step produces and where

## Audit trail

From Git Setup (Step 1) onward — once `$WORKTREE` exists and gitter has inited `$WORKTREE/.checkpoint.json` — the orchestrator appends an audit event alongside each phase line:

```bash
bash .claude/scripts/checkpoint.sh log "$WORKTREE" "{phase}" "{agent(s) that ran}" "{one-line result}"
```

gitter archives it to `$DOCS/audit-trail.json` at MERGE.

## Step 0a — Stale Pipeline Cleanup

The full mechanics of `/wave:builder` Step 0a (MANDATORY pre-flight). The orchestrator reads and executes this before naming the pipeline. Invariants the rest of the pipeline depends on: `BLOCKED.md` dirs are preserved (never archived), wave-owned builds are never archived individually, stale standalone dirs move to gitignored cold storage `tmp/dev/archive/builds/`.

**First, prune orphaned worktrees** — `.worktrees/{name}` directories left by failed or abandoned pipelines that no agent otherwise reclaims (the inverse of the doc-dir sweep below):

```bash
bash .claude/scripts/worktree.sh prune
```

It removes `.worktrees/` dirs that are not registered git worktrees and have no active pipeline docs; registered-but-inactive worktrees are reported, never auto-removed.

Then check for abandoned pipeline directories in `docs/dev/builds/`. A pipeline directory is **stale** if it has NO corresponding active worktree in `.worktrees/`:

```bash
for dir in docs/dev/builds/*/; do
  name=$(basename "$dir")
  if [ ! -d ".worktrees/$name" ]; then
    echo "STALE: $dir (no active worktree)"
  fi
done
```

**For each stale directory found:**

- If it contains a `BLOCKED.md` → it is intentionally preserved (deferred for manual resolution — see § Fix Loop Escalation). **SKIP cleanup.** Do NOT archive, do NOT delete. Print `PRESERVED: $dir (BLOCKED-DEFERRED, awaiting resume)` and move on.
- **If it belongs to an active wave** → **SKIP.** Wave-owned builds are NEVER archived individually — they archive together when the wave archives. Detection: `grep -rl "$name" docs/dev/waves/*/report.md 2>/dev/null`. If any match, print `WAVE-OWNED: $dir (belongs to active wave, skipping)` and move on.
- If it contains a `7-post-merge-qa.md` → it completed but wasn't archived. Archive it to cold storage (see below). **Only for standalone builds (no wave owner).**
- If it has NO completion markers (no `7-*` file, no `BLOCKED.md`) → it was abandoned mid-pipeline. Add an `ABANDONED.md` marker, then archive. **Only for standalone builds (no wave owner).**

```bash
echo "Pipeline abandoned — archived during /wave:builder pre-flight cleanup on $(date -I)" > docs/dev/builds/$name/ABANDONED.md
```

**Archive to cold storage (for standalone builds only — NEVER for wave-owned builds):**

```bash
mkdir -p tmp/dev/archive/builds
mv docs/dev/builds/$name tmp/dev/archive/builds/
```

`tmp/` is gitignored. Files the pipeline already committed stay in git history; if the swept dir was tracked, the next gitter DOCS-COMMIT picks up the deletions.

**Empty directories** (zero files) → just remove them: `rmdir docs/dev/builds/$name`

## BLOCKED.md template (Fix Loop Escalation)

When the fix loop escalates to BLOCKED-DEFERRED, the orchestrator writes `$DOCS/BLOCKED.md` with this template:

```markdown
# Pipeline Blocked: {pipeline-name}

**Status:** BLOCKED-DEFERRED
**Trigger:** {pre-existing-orthogonal | iteration-cap | hung-test | repeat-bug | sub-agent-orphan}
**Date:** {YYYY-MM-DD}

## Root cause

{Specific reason — file:line of hung test, bug ID that wouldn't fix, or sub-agent that died.}

## State preserved

- Worktree: `.worktrees/{pipeline-name}/` (NOT torn down)
- Pipeline docs: `$DOCS/` (all artifacts intact)
- Ports: still allocated in `.worktrees/.ports`
- Branch on main: NOT MERGED

## Resume protocol

Pick the branch by WHERE the blocking defect lives — the Root cause above names it:

**A — defect is pre-existing on `main` or orthogonal to this pipeline's diff** (trigger `pre-existing-orthogonal`; a hung or stubborn bug in code this pipeline never changed):

1. Fix the underlying bug on `main` first (the live-on-main change path). Note the fix commit SHA: `_______________`
2. `cd .worktrees/{pipeline-name} && git rebase main` (or cherry-pick the fix commit) to pick it up.
3. Re-spawn QA only — skip planners/architects/devs (their work is intact in the worktree).

**B — defect is in THIS pipeline's own uncommitted worktree work** (a bug in code/seed/config this pipeline added — it does not exist on `main`):

1. Fix it directly in the worktree (`.worktrees/{pipeline-name}/…`) — no `main` detour, no rebase; the artifact lives only here.
2. Re-spawn QA only.

**Both branches converge:**

4. If QA passes → gitter MERGE → post-merge QA → docs merge (normal pipeline tail).
5. If QA still fails → ONE more fix-loop iteration max, then re-defer.
```

## Pipeline step map

What each `/wave:builder` step produces and where. Each step in `wave/builder.md` is authoritative for its own Produces/Location; this is the at-a-glance index.

**Two-gate test discipline:** developer self-QA (Step 6) and the Step 7 fix-loop rounds are TARGETED (unit + typecheck + lint + only the failing/affected profiles + the pipeline's adversarial tests, NEVER the full suite). The full suite runs at exactly two zero-tolerance gates — **GATE-1** (pre-merge full, on the worktree branches, between Code review and Merge) and **GATE-2** (post-merge full, on `main` after merge). Both gates run on the per-pipeline isolated test stack (`up-test-pipeline` / `db-setup-test-pipeline` / `nuke-test-pipeline` `PIPELINE={name}` on the worktree's allocated per-project ports from `.env.ports`); GATE-2 runs from the project dirs on `main`.

<!-- Install-time: replace `{project}` placeholders with your roster's project directories — one row set per entry, whatever their names. -->

| # | Step | Who | Produces | Location |
| --- | ------------------------------- | ------------------------------------------- | ------------------------------------------------------------------------------------------------- | -------------------------------- |
| 1 | Git setup | gitter (SETUP) | Worktrees, ports, `$DOCS/ports.md` | root |
| 2a | Parallel analysis | child planners (routing-gated) | `$DOCS/1-analysis-{project}.md` | root |
| 2b | Consolidate plan | main-loop session | `$DOCS/1-plan.md` | root |
| 3 | Cross-project arch + research | main-loop session (global `architect` agent for gap-filling review) | `$DOCS/3-architecture.md` (integration contracts + research notes) | root |
| 4 | Child arch + research | child architects | `$DOCS/3-architecture-{project}.md` (docs only, no code stubs, inline research) | root |
| 5a | UI/UX _(conditional)_ | ui-ux | `$DOCS/4-ui-ux-spec.md` | root |
| 5b | DB Architecture _(conditional)_ | db-admin | `$DOCS/4-db-architecture.md` + schema/migration changes in worktrees | root (docs) + worktrees (schema) |
| 6 | Develop | developers (per-project role) | Working code in worktrees + `$DOCS/5-dev-report-{project}.md` | worktrees (code) + root (docs) |
| 7 | Targeted QA _(pre-merge)_ | child QA ({project}-qa wrapper) | TARGETED pre-merge QA feeding the fix loop — unit + affected/failing profiles + adversarial, NOT the full suite. Adversarial tests in worktrees + consolidated `$DOCS/6-bugs.md` (one `## {PROJECT}` section each) | worktrees (tests) + root (docs) |
| - | Fix loop | developers → targeted QA | TARGETED re-run, cap 3. Repeat until `$DOCS/6-bugs.md` = NONE | |
| - | Code review _(pre-merge gate)_ | audit:code-hygiene → architects → devs | `$DOCS/6-code-review.md` (loops until CLEAN, cap 2) | worktrees (code) + root (docs) |
| - | **GATE-1 — pre-merge full** | child QA (FULL, {project}-qa wrapper) | Full suite (unit + integration/e2e), zero-tolerance all-green on the worktree branches; one bounded fix pass + re-run, still failing → BLOCKED-DEFERRED. Writes `## {PROJECT}` sections of `$DOCS/6-bugs.md` | worktrees (tests) + root (docs) |
| 8 | Merge | gitter (MERGE) | Commits + merges to main | |
| 9 | **GATE-2 — post-merge full** | child QA (POST-MERGE, {project}-qa wrapper) | Full suite from project dirs on `main`, zero-tolerance all-green. `$DOCS/7-post-merge-qa.md` (single consolidated file from inline results) | root |
| 10 | Document | main-loop session | Merges into permanent docs; `$DOCS/` stays in place | root |
| 11 | Commit docs + archive | gitter (DOCS-COMMIT) | Commits docs incl. `$DOCS/`, moves it to `tmp/dev/archive/builds/`, commits removal (standalone) | root |
