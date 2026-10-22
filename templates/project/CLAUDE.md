# {PROJECT_NAME} — {PROJECT_TAGLINE}
<!-- OPTIONAL stake: 1–3 lines on what a careless edit costs here — domain scope and safety disclaimers — or delete the line. -->
{DOMAIN_ADJ} assistant tool. No {FORBIDDEN_DOMAIN_OUTPUTS}. {USER_NOUN} retains full {DOMAIN_ADJ} responsibility.

# Vocabulary

- roster: {PROJECT_NAME}'s 1..N projects, connected by {the project's integration boundaries, if any}; a roster of one is the repo root itself — no per-project subdirectories, no cross-project boundaries

<!-- SETUP fills {PROJECT_ROSTER} with one entry per roster entry, in this shape:
- {project}: {PROJECT_ROLE} — {PROJECT_STACK} · `{project}/`
A single-project install emits one entry per top-level directory git tracks; a multi-project install one per roster entry, plus one per other top-level directory. Do NOT hard-code a project count anywhere.
A roster entry that is the wire-contract/schema hub carries one more clause on its entry: "A wire change starts at `{the hub's consumer-index command}` there — it lists the producer/consumer anchors; edit from that list, never from a tree grep". Drop that clause for a roster with no hub. -->

{PROJECT_ROSTER}

- child CLAUDE.md: a roster entry's own conventions and code placement, beside its agents and skills · `{project}/CLAUDE.md`, `{project}/.claude/`
- fleet prompt: the main chat's system layer — voice, § Model Selection, § Orchestration — written by `pfm install` from its binary's embedded parts into the clone's gitignored `generated/` · `{BLUEPRINT_CLONE_PATH}/pfm/harness-prompts/`
- permanent docs: the reference docs the main loop or the `documenter` agent writes · `docs/_index.md` and the clusters it links, each roster entry's `{project}/docs/`

<!-- OPTIONAL docs map: add entries like these if the project keeps clustered reference docs, or delete the block.
- docs hub: links every architecture, API, system-map, feature and child-project doc; a cluster is read `_index.md` first, then grepped for the exact code/DB symbol — doc identifiers match code verbatim · `docs/_index.md`
- DB diagram: every table, column and FK under its real {DATABASE} name · `docs/graph/db/postgres.mmd`
- facts: invariants the user has ruled; read before touching data lifecycle, {SENSITIVE_DATA} or an external service; code contradicting a fact → escalate, never edit either side · `docs/facts/_index.md`
- runbooks: how-to per project · `docs/runbooks/{project}/`; features `docs/features/`; runtime reference cards `docs/references/`; business `docs/business/` (`marketing/`); legal and compliance `docs/epics/legal/`
- Code truth: grep the code. Schema truth: introspect the live DB. -->

## Path vars

- `$CDOCS`: `docs/commands`
- `$REFS`: `references`
- `$RESEARCH`: `research`
- `$RESOURCE`: `resource`

# Runtime

<!-- OPTIONAL Codex: delete this section if you do not use OpenAI Codex — everything works with Claude Code alone; it is for projects that run a second runtime for cheaper implementation. -->

## Codex

- `CLAUDE.md` and `AGENTS.md` are one shared contract; Claude and Codex both carry the persona and rules, and runtime-specific wrappers translate only mechanics (slash commands, agents, git execution), never identity or protocol.
- The full two-runtime protocol: `.codex/README.md`.

<!-- END OPTIONAL CODEX SECTION -->

## Local

- Scratch lives in `/tmp/{project}/{purpose}/`, outside the tree: `{project}` is this repo's directory name minus any leading dot, derived, never hardcoded.
- One scratch subdirectory per purpose, each owned by a named protocol (`/tmp/{project}/dev/`); a scratch path named to a human or a model is absolute.

# Rules

## Sacred ground

- **No secrets in any code:** keys live in `.env.*`.
- **NEVER edit code on `main`:** worktree branches only, gitter-merged after its gate passes; a change made on `main` by explicit command still gets its gate pass afterwards.
- **Only gitter writes git:** commit, merge, checkout, branch, stash, reset, push and every other state-changing git are gitter's for every agent; read-only git (status, diff, log, show, rev-parse) is open to all.
- **NEVER commit broken code or merge before the gate passes.**
- **Only the main loop or the documenter agent it dispatches writes permanent docs,** under the `/quality:doc` Approval gate; `docs/epics/legal/` belongs to `/officer`, `docs/business/` to `/mentor` and `/marketer` (`marketing/`), and `docs/facts/` to the main loop solely on the user's explicit ruling.

## Dispatch

- Tiers, effort and delegation posture live in the fleet prompt's § Model Selection — never restated here.

## Code

- Every `catch`/`except` logs the full stack trace; nothing is swallowed.
- An error never renders as absence: absence is a claim about the world ("no data exists"), an error a claim about ourselves ("we failed to look"); every empty, no-data or degraded state — UI, health check, gate verdict — distinguishes the two.
- A {DOMAIN_ADJ} surface that shows "no signal" while the signal sits in the DB is a silent false negative on {DOMAIN_ADJ} data: the {USER_NOUN} is misled by a screen that never admits it broke; logging the error is necessary and NOT sufficient — the visible state tells the truth.
- AI-generated content is marked at the rendered surface: verify the component that displays it, not the data hop that carries the flag; a fetched-but-unrendered marker is unmarked AI prose in a {USER_NOUN}'s hands.
- Read data before asserting on it — existence or count proves nothing: "{SUBJECT_NOUN} stated:" over a quote whose `speakerRole` says {USER_NOUN} puts the {USER_NOUN}'s words in the {SUBJECT_NOUN}'s mouth, in the {RECORD_NOUN}; the type system cannot see it — the field is present, typed, and simply never read.
- Validate data where it enters — jsonb columns, LLM output, external payloads — with a parser (Zod, pydantic), not an `as` cast, which blinds `tsc` to the exact nullability mismatch that crashes at the first real row.

<!-- KEEP the next rule only if the roster has a project with its own SQL/migrations directory; drop it for a roster with no database. -->

- SQL lives in one place, `{MIGRATIONS_DIR}` migrations: no `.sql` file exists in any other project.
- Surgical changes: every changed line traces to the task; leave working adjacent code unrefactored and unrenamed; fix broken code you hit; remove dead code and unused deps end to end, as if they never existed.
- Placement: code goes where the child CLAUDE.md places it; match the existing naming and structure; a new directory or pattern only when the task requires it.
- No duplication: grep for the existing function, component, hook, type or util and import it; extract and call instead of keeping a near-copy.
- Right-size and finish: the simplest thing that works; no speculative abstractions; complete, no stubs (`NotImplementedError`, a lone `...`, a deferred TODO); import only manifest packages, and install only validated libraries.

## Process

<!-- KEEP the next rule only if the roster has a project that owns infra/orchestration (its directory is `{PROJECT}`); drop it for a roster with no such project. -->

- Infra ops go through the owning project's `Makefile` (`make -C {PROJECT}`), not direct `{CONTAINER_RUNTIME} exec` / `{DB_CLI}` / `{CLOUD_CLI} {QUEUE}` calls.
- Prompt files: a task touching `.claude/**` or any `CLAUDE.md` routes to the `pcm` agent (the user's `/pcm` dispatches it), which loads `/quality:prompt` before the first edit.
- Worktrees are costly: batch a session's related changes into one, and ask before creating one.
- CI verifies, it does not debug: reproduce and fix locally, then trigger CI.

## Meta

- Three lenses at once — Computer Science, {DOMAIN_NOUN}, Regulatory Compliance (`/officer` for formal assessment); the intersections carry the value — {DOMAIN_RISK_EXAMPLE}.
- AskUserQuestion is the user's whole screen — chat prose between dialogs never reaches them: context travels inside the question text; a clarification gets its answer in the next question's title, simpler and more concrete each round, never a rephrase.
- When in doubt, do the right thing: the correct path over the convenient, even at the cost of re-architecting.
