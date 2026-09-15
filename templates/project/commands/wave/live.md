---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: wave:live
description: Batches a task list on `main` — no worktree; parallel builds, {project}-qa tests per touched project, one docs pass + gitter commit, then /wave:walker with inline remediation. Trigger /wave:live [file|tasks] (empty → root wave.md). Successor /wave:walker (merge-SHA mode).
argument-hint: [task file | inline tasks]
---

# Wave Live — Batch Execution on `main`

Run a batch of tasks live on `main`: $ARGUMENTS

## Overview

A task list runs here. The fix machinery the steps below cite is the fix-core card, `$CDOCS/wave/$REFS/fix-core.md`; missing or stale → say so and stop rather than improvising a fix loop.

## W1 — Resolve, stage & pre-flight

**user-question forecast (gate):** enumerate and CLOSE every user-only item the batch will hit (secrets, deploy reviews, destructive ratifications, merge nods) before W2 — a mid-batch wait on the user is a failed pre-flight and a reversal retro.

**Resolve the task list:** empty/blank arg → task file is `wave.md` at repo root; a path → read that file; a description → parse as inline tasks. Wave-train partitioning (splitting a multi-area spec into per-area waves) is orchestrator-only — `/wave:live` always flattens a partitioned spec into one flat batch.

**Pre-flight fatal (before any work starts):** grep every named entity the tasks reference — components, tables, endpoints, files; a referent that doesn't exist, conflicting edits to one target across tasks, or an unorderable dependency stops the wave here — diagnostic printed, no dir created.

**Stage the wave directory:** choose a short kebab-case `{wave-name}` (2–4 words) for the theme; on a collision with `docs/dev/waves/` or `tmp/dev/archive/waves/` append `-v2`; `mkdir -p docs/dev/waves/{wave-name}` and copy the resolved spec to `docs/dev/waves/{wave-name}/manifest.md` — the wave's permanent record. When the source is the root `wave.md`, reset it to the `# Tasks` stub in the same step so the consumed spec never lingers at root; a custom-path task file is copied, not cleared. Read the spec from the manifest thereafter, and extract `**Epic:** {name}` from it (`none` if absent).

## W2 — Group for filesystem safety

`main` has no worktree isolation — two agents editing one file corrupt it, with no merge to resolve. Run tasks concurrently ONLY when their file sets are disjoint and they share no mutable resource (package manifest, migration/schema, env file, the running dev server). Serialize tasks that share a file, depend on another's output, or mutate a shared resource, ordered by dependency. Uncertain disjointness → serialize.

## W3 — Execute on `main`

Spawn one implementation agent per task, briefed with five fields: the goal and the artifact it returns, the boundary of what is and is NOT in scope, the exact files and symbols to start from, the tier and effort, and what its own failure looks like. Each brief carries the task's exact section, the files it owns, the project's child `CLAUDE.md`, and "implement code only — the QA phase writes the tests." Run disjoint agents concurrently in ONE message; run serial tasks one at a time, re-checking each task's assumptions against the prior result before it starts. Implementation follows the fix-core card § Step 3 (Build with sub-agents — adapt to the project's structure — and Rules while fixing); each agent typechecks its own project before returning.

Dispatched agents and reports received must match, and the count appears in the W7 report. A missing report is a named coverage hole, never a silent omission.

## W4 — QA writes the tests

The full suites run on the single-tenant canonical test stack: take the boundary mutex `tmp/wave-boundary.lock` for the W4 span (atomic `mkdir`; write `{wave-name} {PID} {timestamp}` to a `holder` file inside; release at W5) — a held lock is another seat's gate, so wait for its release rather than squatting the stack behind it. Once every task has landed, spawn each modified project's registered `{project}-qa` agent in POST-MERGE mode — tests run against `main`, no worktree paths, findings reported in the return. Each adds the regression + unit coverage for this wave's changes in its project and runs the full suite under the fix-core card § Step 4c zero-tolerance — every failure blocking, pre-existing included. A regression test counts only after it was watched FAILING against the unfixed code; a filtered or skipped run is a named gap, never a pass. Fix all breakage before proceeding.

## W5 — Cleanup → docs → commit

1. **Cleanup** — the fix-core card § Step 5 format + lint gate on every modified project.
2. **Docs** — run the fix-core card § Step 6 ONCE over the whole batch and every affected project. If `{epic-name}` is not `none`, consolidate the wave into `docs/epics/{epic-name}/` per `docs/epics/TEMPLATE.md` § Consolidation contract.
3. **Commit** — invoke `gitter` (the fix-core card § Step 7, Phase COMMIT): one code commit per task or logical group, plus one doc commit.

## W6 — Review & remediate

Write a lightweight review input to `docs/dev/waves/{wave-name}/review.md` — the manifest's task list plus the W5 commit SHAs (the walk diffs `{sha}^..{sha}` for each). Run `/wave:walker docs/dev/waves/{wave-name}/review.md` (merge-SHA mode): it dispatches `tracer` and `reviewer`, folds both, and writes `## Professor's Wave Review` — verdict, findings, `### Action Items` — into that file.

Group every code finding in `### Action Items` by its file or project (a finding with no single owner file groups by its named project). Run ONE remediation lane per group — diagnose → fix every finding in the group → re-test that group's affected suites once → cleanup (the fix-core card §§ 2–5) — then `gitter` `Phase: COMMIT` for the group: one commit per group, or one commit total when every group lands together. Re-run the fix-core card § Step 6 if a fix changed documented behavior. Surface the review's owner-tagged deferrals (`/officer`, the user); never park a fixable defect. Present the verdict.

## W7 — Report

Report per the fix-core card § Step 8 (Fix format), adding a per-task table (task → files changed → tests added → commit) and the review verdict. Persist the same report to `docs/dev/waves/{wave-name}/report.md`.

## W8 — Archive the wave directory

Invoke `gitter` ("Wave: {wave-name}. Phase: DOCS-COMMIT. Archive: docs/dev/waves/{wave-name}.") to commit the manifest, review, report, and the root `wave.md` stub reset into history, then move the directory to `tmp/dev/archive/waves/{wave-name}/` and commit the removal — the wave's record lives in git history and cold storage, never lingering in `docs/`. A source spec consumed from the refine queue (`docs/dev/trains/queue/`) archives in the same call — name it in `Archive:` too (→ `tmp/dev/archive/waves/queue/`). Verify `docs/dev/waves/{wave-name}` is gone and `tmp/dev/archive/waves/{wave-name}` exists.
