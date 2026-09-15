---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: wave:refine
description: Writes ONE zero-gap wave spec — to docs/dev/trains/queue/{date}-{slug}.md, asking only what the code cannot answer. Chain head — refine → /wave:orchestrator → /wave:builder → /wave:walker. `poc <goal>` refines AND builds under .professor/RND/POC/{name}/; merge mode (scheduler-invoked, non-interactive) unifies two+ specs into one. Triggers "refine", "refine this/tasks/poc".
argument-hint: [tasks | poc <goal>]
---

# Refine — write the wave spec

One deliverable: `docs/dev/trains/queue/{YYYY-MM-DD}-{slug}.md` — the complete executable spec of ONE wave. ZERO GAP: every field, column type, signature, file path, route, behavior, and copy string is decided here. Downstream (the scheduler agent, `/wave:orchestrator`, builder) executes the spec verbatim and never re-decides; a gap found downstream routes back here as a delta, never gets filled there.

## A wave is a FEATURE

A wave carries every layer of one feature together — contracts, schema, and every roster project the feature crosses. A feature is never split by project layer. Tasks are ordered INSIDE-OUT — schema and contracts first, then each project outward to the user-facing surface — so each layer lands on something that already exists. Work belonging to a different feature = a separate spec; the scheduler agent owns ordering and merging across specs.

## R1 — Walk the code

Read: root `CLAUDE.md`, `docs/facts/_index.md`, child CLAUDE.md of the touched projects, `docs/epics/legal/manifest.md` _(when the Officer archetype is installed)_; grep how the roster's projects connect on the touched subsystems; introspect the live DB for canonical names. Fan out read-only `tracer` agents, one per subsystem cluster, each returning per-task cards with file:line evidence:

- Code referenced: paths/components/chains the task names or implies; nonexistent = say so. A path cited as a data source is opened to confirm it contains that data. A production behavior a task relies on as a precondition — an "existing" code path, a CLI verb or flag, an exit code — is verified in code at card time and cited with its anchor; absent = the wave gains the task that builds it, ordered before its dependent.
- What exists today / what's missing.
- Reuse targets: existing helpers/components/types to import, never rebuild.
- Incumbent patterns: how this codebase already solves the task's mechanism classes.
- Status: `READY` / `NEEDS-CLARIFICATION` / `NEEDS-USER-SPEC`.

Tracers retrieve raw maps; you judge and author.

## R2 — Ask the user until clear

`AskUserQuestion` rounds, ≤4 questions per call. First always: (a) `NEEDS-USER-SPEC` tasks — spec / defer / drop; (b) scope boundary — restate the user's full objective and confirm what this wave includes vs defers; scope never narrows silently. Ask nothing derivable from code. Continue until every task is clear or the user disposes it (defer/drop) — proceeding unclear is not an option. Forecast every user touchpoint the wave will EVER need (secrets, deploy reviews, destructive-op ratifications, merge nods) and record each as pre-authorized in the spec — a wave that stops mid-flight for a user answer is a failed spec.

A task needing prototype evidence before it can be specified runs `refine poc` or the project-scope `/rnd`, which executes the run itself; the spec embeds the findings (§ RND findings).

## R3 — Decide and write

Settle every technical branch before writing: transport, data placement, mechanism, failure modes, migration. Every mechanism decision first names the incumbent pattern it follows; deviating carries a one-line justification. A branch with 2+ defensible options and materially different consequences goes to the user; everything else you settle. Per task, decide and write:

- **Routing** — the exact roster project set the task touches + the conditional build agents (`{project}-db-admin` on data-model change, `{project}-ui-ux` on visual work).
- **Data model** — every table, column with exact type, index, enum, constraint.
- **Contracts** — exact API schema, resolver/handler signatures, queue message schemas, realtime event payloads.
- **File plan** — every file to create/edit with the functions/exports it gains and their signatures. DELETE only for grep-verified single-purpose files; otherwise `EDIT (strip X by def-boundaries)`. A dropped or reshaped column/enum/table names its full coupling including raw-SQL string references; a removed config field names its env-var scrub set. A removal spanning >10 files or >3 layers is declared a fan-out candidate. A change crossing a wire boundary lists every consumer the contracts hub's consumer index returns for the symbol — the file plan covers that list, not a tree grep.
- **Behavior** — success/failure/edge paths, UX, copy, scope. Every new check names what it reports when the check itself is broken.
- **Sensitive-data channels** — every place the task moves protected domain content. Content reaches the access-controlled DB and nowhere else; a clause routing it to a log, metric label, error string, or telemetry payload surfaces at the R4 gate as its own plain-words line or does not ship. Escalations carry the pointer, never the text.

### Spec format

Header: `# Wave: {slug}` · `**Status:** QUEUED` · `**Refined:** {YYYY-MM-DD} · main @ {short-sha}` (the HEAD walked in R1 — the scheduler agent's staleness anchor) · `**Epic:** {name | none}` · `**Touches:** {project set}` · `**Scope:**` · `**Deferred:**`. Then `## Task Reconciliation` (Original | Disposition | New # | Notes — every original task traces to REFINED / MERGED INTO #N / DEFERRED / DROPPED). Then, on a wave carrying data-model change, `## Data-model ↔ contract reconciliation` (Schema change | Contract owner or INTERNAL-ONLY | Live-row disposition): every table/column change — add, drop, reshape — names the contract task owning its wire shape, or INTERNAL-ONLY plus the scope searched to prove it; where the table holds live rows, the row names its disposition — carried / re-derived by a named trigger / accepted loss with the ratifying touchpoint ("a future run re-derives it" without a named trigger is not a disposition). Enumerated from the schema side: a per-task review cannot detect a missing owner — the defect is the absence of an entry in another task. Then tasks in inside-out order, `### Task #{N} — {title}` separated by `---`, each with exactly these sections (`none` allowed):

1. One-line summary + `[WATCH: ...]` flags, then `**Why:**`
2. `**Routing:**` + `**Build agents:**`
3. `**Key behaviors:**` lettered success/failure/edge paths
4. `**Data model:**`
5. `**Contracts:**`
6. `**File plan:**`
7. `**Boundaries & anchors:**` what's NOT included + existing files/identifiers to reuse, every parity claim with its exact anchor. Every fact the builder's hand depends on is quoted here with its `file:line` — a pointer to a ledger, walk-notes or evidence directory in place of the quoted fact is a gap; the hand's first command is its target file.

Rules blocks binding a subset of tasks sit above those tasks; all-task rules sit in the header. `[CMD: /name]` tags route non-builder tasks to the named command. Tag `[MILESTONE]` on checkpoint task headings.

### RND findings (when RND/POC fed the wave)

Write `## RND-Validated Mandatory Rules` before the task list: every validated prompt in a fenced block, byte-identical — never paraphrased (a rewritten prompt is an unvalidated prompt) — labelled with where it runs; every technique that separated success from failure, with numbers; every behavior adding an LLM call names its validated prompt artifact or is staged to the wave where its prompt validates. Every open item the RND record still carries — its build items, rulings owed, trace findings — is disposed one by one before the spec queues: folded into a task line naming the mechanism (never a generic phrase), deferred with a `[WATCH: …]` tag and a reason, or dropped with a one-line justification; silent omission is not a disposition. A task's annex cites the file that carries each requirement, never only the settled summary beside it.

## R4 — Review + user gate

- **Officer (MANDATORY on sacred ground):** a wave touching sensitive data, consent, retention, auth, or a role boundary gets a fresh-context `/officer` Advisory pass (opus agent); its flags fold in as `[WATCH:]` tags. Anything mandating a new consent scope or schema goes to the user, never auto-encoded. _(When the Officer archetype is installed.)_
- **Architect (always):** `Agent(subagent_type: "architect")` on the spec path in fill mode: it corrects every gap in the file and recurses (fresh architect per pass) until a pass declares NO MORE GAPS; the refiner reads the `## Open decisions` list from the deltas block and takes it to the user gate, and folds the ratified answers as targeted edits. No hand-fold/re-run loop.
- **User gate:** present the Scope/Deferred boundary plus one line per task (routing + the key technical and product decisions made for them). Loop until approved; approval queues the spec. Running the train (`/wave:orchestrator`) is their separate decision.

**Legal fence (sacred):** DPIA, DPA, RoPA, privacy policy, consent docs, anything under `docs/epics/legal/` or of legal character — you never edit one and the spec never carries a task or clause ordering any agent to; a paper need is listed in the R4 summary as a user-owned item.

**Merge mode (scheduler-invoked):** input = two+ already-approved specs ruled one feature. Produce the unified spec non-interactively (no AskUserQuestion — you run inside an agent): union the tasks, re-order inside-out, reconcile overlaps (one reconciliation table covering both sources), keep every RND rule block. A genuine contradiction between the sources is returned as a question for the scheduler's user gate, never settled silently.

## Subcommand: `poc <goal>`

Interrogate a proof-of-concept idea into an airtight spec, then build it directly — disposable sandbox under `.professor/RND/POC/{name}/`, no gitter, no QA gates, no worktree.

1. **P1 — Scope:** read only the code the POC exercises or stubs; decide real vs faked.
2. **P2 — Ask the user** one focused batch: what it must prove, the observable success signal, real vs faked, scope boundary, stack. Loop until clear.
3. **P3 — Spec:** write `.professor/RND/POC/{name}/spec.md` — Goal, Proves, Success criteria, Real vs faked, Build plan (every file + signatures), How to run, Boundaries.
4. **P4 — Build:** dispatch sonnet agents per the build plan (independent probes in parallel); report the result.
