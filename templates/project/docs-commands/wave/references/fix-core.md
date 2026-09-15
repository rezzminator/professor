# Fix Core — the fix loop (Steps 2–8)

> The fix loop cited by `/wave:live` — Diagnose, Fix, Verify, Cleanup, Docs, Commit, Report.

---

## Step 2 — Diagnose

**Hang / deadlock / mystery failure** (0%-CPU hang, no-output-no-error, intermittent or 1-in-N flake, passes-alone-fails-in-suite, silent crash) → read `$CDOCS/wave/$REFS/debug-discipline.md` and follow it before any other diagnosis.

Based on the investigation:

1. **Root cause** — trace from symptom to source.
2. **Affected files** — list every file that needs changes.
3. **Plan** — what changes, in which order.
4. **Risk** — what else this fix could break.

For cross-project issues (roster size > 1), trace the full path across the boundary the projects share — e.g.:

- **client → server:** UI query → {API_PROTOCOL} resolver → service → DB
- **server → {AI_SERVICE_NAME}:** {API_PROTOCOL} mutation → {QUEUE} publish → {AI_SERVICE_NAME} consumer → chain → DB
- **{AI_SERVICE_NAME} → server:** {QUEUE} response → listener → DB update → {REALTIME_PROTOCOL} push

At roster size 1 there is no cross-project hop — trace within the single project.

---

## Step 3 — Fix

Apply the fix directly on `main`, with edit access to every roster project's source (root CLAUDE.md § Architecture) plus `.env.local` / `.env.test`.

### Build with sub-agents

Build multi-part work with sub-agents, not inline — decompose into parts and spawn one implementation agent per part; your accumulated context biases the build, and a clean agent with a precise brief is faster and more accurate. Parts in **different roster projects** with no shared files run in **parallel** (one message, multiple agents); parts that share a file or depend on another's output run serially in dependency order — on `main` there is no worktree isolation, so two agents must never edit one file at once. Brief each agent with its exact files, task slice, and the project's child `CLAUDE.md`. A trivial single-part fix you may apply directly.

**Always adapt to the project's structure** — before writing, read how the project already does this (layout, naming, patterns, existing utilities) and extend it; reuse before writing and follow placement conventions (root CLAUDE.md). Building that ignores the project's shape is a defect, not a delivery.

### Rules while fixing

- **Follow each project's code standards** — read the child CLAUDE.md if unsure
- **Use structured loggers** — never raw `console.log`. Use the project's logger module
- **Never log {SUBJECT_NOUN} data** — anonymized IDs only
- **Keep changes minimal** — fix the problem, don't refactor the neighborhood
- **Honor each project's type discipline** — strict typing, no escape hatches (e.g. `any`/`Any`) without justification, per the project's language
- **New dependencies are allowed** — validate the library first (root _Never install unvalidated libraries_ rule), then add it to the project manifest before importing
- **Nuke dead code** — removing a feature removes ALL references (interfaces, implementations, service methods, test mocks, types) in the same commit; dead code misleads future readers

### Server management during fixes

Restart a changed service with `/dev restart {project}` (a hot-reloading dev server usually needs no restart). After DB schema changes, run migrations first. If the fix came from `/dev` auto-heal, restart with `DEV_NO_AUTOHEAL=1` so the `/dev` loop cannot repeat.

---

## Step 4 — Verify

After applying the fix:

### 4a. Restart affected servers

Use `/dev restart` or restart individual services as needed.

### 4b. Check logs for errors

After the restart settles, check for new errors via `/dev log` (or tail `$ROOT/tmp/dev/*.log`).

### 4c. Test the fix

- Hit the relevant endpoints to confirm the issue is resolved.
- **Affected-first:** run only the tests you touched or added (plus directly affected ones) first as a fast confirm — they must fail without the fix and pass with it. Only once they pass, run the **full** suite (unit + integration) once per modified roster project, as the gate. Derive each project's suite commands from its child `CLAUDE.md` and its qa-reference doc — a project's integration tier can be a separate set of scripts from its unit tier, so the top-level test command alone may not be the full gate.
- **ZERO TOLERANCE — fix ALL failing tests,** whether your fix caused them or they were already broken; a pre-existing failure is a second bug you just found — diagnose it, fix it, ship it in this commit. The ONLY exception: a test requiring an external service you genuinely cannot reach (an unconfigured paid API key) — document that skip explicitly in your report.

### 4d. Run typecheck

```bash
# PATTERN — per modified roster entry, run that project's typecheck (e.g. `run build`, `tsc --noEmit`, `run mypy src/`)
cd {project} && {PROJECT_TYPECHECK} && cd ..
```

Only run checks for projects that were modified. Skip projects whose language has no separate typecheck step.

### 4e. If the fix didn't work

Return to Step 2 and re-diagnose with the new information; iterate (logs, breakpoints, endpoint tests, DB inspection) until the issue is resolved.

### 4f. Prevent recurrence

After the fix is verified, ask: **"Can this class of bug happen again?"** If yes, harden the codebase so it can't. Choose the lightest measure that actually prevents recurrence:

- CLAUDE.md convention — an agent could rewrite the fix away: add the rule to the relevant child CLAUDE.md so agents preserve the pattern.
- Type guard — a wrong type crossed a boundary: strict types or runtime validators (in the project's language) that reject the bad input.
- Lint rule / assertion — the pattern could recur anywhere: a project-level lint rule or runtime assertion.
- Config / env default — a missing or wrong config value: a sensible default, startup validation, or a fail-fast check.

Every fix carries at least ONE prevention measure, committed alongside it in the same commit — "just fixing it" is not enough. A genuine one-off (typo, wrong constant with no pattern) states why none is needed rather than skipping silently.

### 4g. QA regression test

Always invoke `Agent({project}-qa)` — the modified project's registered QA subagent, one per modified project — to add two layers of coverage: a regression test that reproduces the failure end-to-end (fails without the fix, passes with it), and unit tests for the specific functions, components, or sections that broke. QA judges feasibility — when no reliable test is possible (e.g. an external-service-only failure), it reports why instead of forcing one. Both ship in the same commit.

---

## Step 5 — Cleanup

Before committing, ensure the codebase is clean:

1. **Remove debug artifacts** — any temporary `console.log`, `print()`, hardcoded values, or test hacks added during investigation (keep intentional logging additions).
2. **Verify servers are healthy** — run `/dev status`.
3. **Stop dev servers** — run `/dev kill` to ensure clean state.
4. **Format + lint gate** — zero lint errors on every modified project, fixed before committing:

```bash
# PATTERN — per modified roster entry, run that project's formatter + linter
cd {project} && {PROJECT_FORMAT} && {PROJECT_LINT} && cd ..
```

---

## Step 6 — Update permanent docs

Docs update BEFORE committing — Step 7 ships code + docs in one gitter call. First spawn a collector-tier doc-relevance classifier briefed with the diff's file list + a one-line change summary, schema-forced to return exactly `{docsAffected: true|false, scopes: [affected doc clusters]}` — it classifies only, never concludes. `docsAffected: true`, or ANY uncertainty, → the main-loop session merges the change into the permanent docs it owns (root clusters under `docs/agents/`, each roster project's `docs/`); a command-owned surface (`docs/business/**`, `docs/commands/{cmd}/`) is updated only by its owning command, and `docs/facts/` only on the user's explicit ruling.

Merge rules: load `/quality:doc` first; verify every operation/table/component name against the changed source, never the dev report; edit only the docs the diff reaches, current-state (superseded text is replaced, git history keeps it); run the `/quality:doc` Approval gate over every doc touched — fix-and-recheck until `APPROVED: {path}`; then `npx prettier --write --prose-wrap preserve` on each edited `.md`. Docs are not committed here — that happens in Step 7.

`docsAffected: false` is legal only for zero-doc-surface changes (comment typo, log-message string, cosmetic-only); report "Docs skipped — no doc surface (classifier + {reason})". Any change that adds/removes/renames a function, changes a config constant, modifies a data flow, or alters test patterns HAS doc surface — a classifier verdict to the contrary is wrong; update the docs.

---

## Step 7 — Commit all changes via gitter

Invoke the `gitter` agent ONCE with `Phase: COMMIT`, `Pipeline: {wave-name}` (or the caller's pipeline name), the project keys held, the exact code files changed, and the exact doc files changed (or "none — docs skipped"). Gitter stages only the files you name, lands one code commit plus a separate doc commit when docs changed, and reports the hashes. Name every file — an unnamed file does not ship.

---

## Step 8 — Report

### Diagnostic (read-only)

Match the shape to the query, always with `file:line` references:

- **Trace** — each hop as `[Component] file:line — what happens`, with the data shape between hops.
- **Locate** — `Found: file:line` + purpose + how it fits.
- **Diagnose** — workflow name, then failure points ranked by likelihood (`file:line — what fails, why`), then what to check first.
- **Data** — tables/lists with source refs.
- **Scope** — direct deps, transitive deps, blast radius (`N files across M projects`), risk LOW/MEDIUM/HIGH.

Close by stating no changes were needed.

### Fix (read-write)

- Problem: {what was wrong} · Root cause: {file:line}
- Fix: {what changed} · Prevention: {what stops recurrence}
- Tests: {pass/fail — suites} · Commits: {hashes} · Docs: {list or "none — trivial"}
