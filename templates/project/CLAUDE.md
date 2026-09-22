# {PROJECT_NAME} — {PROJECT_TAGLINE}

## Spawned as a sub-agent? Your first move

Before your first tool call, count the tasks in your brief. A task is one deliverable with its own files and its own acceptance check; items landing in the same file or the same small module are one task, however many bullets list them.

- A brief naming a task file: open it together with the shared files named beside it, in your first message, and execute it.
- One task you can see how to do — the brief plus one look at the target tells you which files change and how: do it yourself, start to finish.
- Anything else — a task you cannot see how to do (a failure with an unknown cause, a design to choose, files you cannot name) or a batch of several tasks: your first tool call spawns `flights-speccer` (Agent tool, `subagent_type: flights-speccer`), handing it the work, everything you already hold and a directory under `/tmp/{project}/flights/`. Its return is your orders: a directory of one task file you execute yourself; of several, you execute none and hand the directory to `flights-orchestrator` (Agent tool, `subagent_type: flights-orchestrator`), which runs one fresh executor per task file and returns once. Below opus you never write a spec yourself.
- ✓ "Fix these five things in `ledger.mjs` and update its README" is one task: read it, fix it, test it — no `flights-speccer`, no spawn.
- ✗ "Take the four failing test lanes to green" done by one agent: hundreds of calls, each re-sending a context grown past 400K. ✓ `flights-speccer` first, then `flights-orchestrator` runs one executor per task file.
- Waiting is one call: an explicit `timeout` up to the maximum, or one blocking wait — never a no-op command, a repeated log peek or a `sleep` chain.

> **Domain Scope (optional):** Add domain-specific scope/safety disclaimers here, or delete the block. _Example:_ "{DOMAIN_ADJ} assistant tool. No {FORBIDDEN_DOMAIN_OUTPUTS}. {USER_NOUN} retains full {DOMAIN_ADJ} responsibility."

**Architecture:** {PROJECT_NAME} is a roster of 1..N projects connected by {the project's integration boundaries, if any}.

<!-- SETUP fills {PROJECT_ROSTER} with one bullet per roster entry, in this shape:
- `{project}/` — {PROJECT_ROLE}: {PROJECT_STACK}
A single-project install emits exactly one bullet (or drops the list and names the repo inline). A multi-project install emits one bullet per entry. Do NOT hard-code a project count anywhere.
A roster entry that is the wire-contract/schema hub carries one more clause on its bullet: "A wire change starts at `{the hub's consumer-index command}` there — it lists the producer/consumer anchors; edit from that list, never from a tree grep". Drop that clause for a roster with no hub. -->

{PROJECT_ROSTER}

Each project with its own `.claude/` carries a `CLAUDE.md`, agents, and skills. A single-project install (roster of one) is the repo root itself — no per-project subdirectories, no cross-project boundaries.

**Docs map (optional):** Add a pointer index like this if the project keeps clustered reference docs — _example:_ "start at `docs/agents/_index.md` — the hub linking every architecture, API, system-map, feature, and child-project doc. Reference docs are **clusters**: read the cluster `_index.md`, then `grep` it for the exact code/DB symbol and open the matching topic file. Doc identifiers match code verbatim, so a code symbol greps straight to its doc. The whole database — every table, column, and FK under its real {DATABASE} name — is one diagram: `docs/agents/graph/db/postgres.mmd`." _Example (facts registry):_ "System facts — invariants the user has ruled — live at `docs/facts/_index.md`; read them before touching data lifecycle, {SENSITIVE_DATA}, or an external service; code contradicting a fact = escalate, never edit either side." _Example (truth hierarchy + doc trees):_ "Code truth: grep the code. Schema truth: introspect the live DB. How-to: `docs/runbooks/{project}/`. Feature registry: `docs/features/`; runtime reference cards: `docs/references/`; business/marketing: `docs/business/` (`marketing/`); legal & compliance: `docs/epics/legal/`." Delete the block if the project has no such registry.

<!-- DELETE THIS SECTION if you are NOT using Codex (OpenAI). If you ARE using Codex, fill in the details and remove this comment. -->

---

## Two-runtime team — Claude + Codex (OPTIONAL)

> **Skip this entire section if you don't use OpenAI Codex.** Everything works with Claude Code alone. This section is for projects that want a second runtime for cheaper implementation.

This project runs two AI runtimes as a team. Full protocol: `.codex/README.md`

**Quick ID:** `CLAUDE.md` and `AGENTS.md` are the same shared contract. Claude and Codex both carry the persona and rules; runtime-specific wrappers only translate mechanics (slash commands, agents, git execution), never identity or protocol.

<!-- END OPTIONAL CODEX SECTION -->

---

## Persona

Voice and delivery law live in Professor's harness prompts under `pfm/harness-prompts/`: `share/head.md` and `share/tail.md` wrap each engine's own `{claude,codex,opencode}/professor.md`, and `pfm install` composes one prompt per engine. Claude's `production` mode uses its native prompt.

## Path vars

- $CDOCS: docs/commands
- $REFS = references
- $RESEARCH: research
- $RESOURCE: resource
- Scratch: `/tmp/{project}/{purpose}/` — outside the tree, never the repo. `{project}` is this repo's directory name with any leading dot stripped, derived, never hardcoded; one subdirectory per purpose, each owned by a named protocol (`flights/`, `timing/`, `guard/`). A scratch path named to a human or a model is absolute.

## MANDATORY Rules

### Code

- No secrets in any code — keys in `.env.*`
- Never swallow exceptions: every `catch`/`except` logs the full stack trace
- **An error never renders as ABSENCE** — absence is a claim about the world ("no data exists"); an error is a claim about ourselves ("we failed to look"). Every empty/no-data/degraded state — UI, health check, gate verdict — distinguishes the two, and a {DOMAIN_ADJ} surface that shows "no signal" while the signal sits in the DB is a silent false negative on {DOMAIN_ADJ} data: the {USER_NOUN} is misled by a screen that never admits it broke. Logging the error is necessary and NOT sufficient; the visible state must tell the truth.
- **AI-generated content is marked at the RENDERED SURFACE** — verify the component that displays it, never the data hop that carries the flag; a fetched-but-unrendered marker is unmarked AI prose in a {USER_NOUN}'s hands.
- Never assert by only the existence or count of data, read it: ("{SUBJECT_NOUN} stated:" over a quote whose `speakerRole` says {USER_NOUN} puts the {USER_NOUN}'s words in the {SUBJECT_NOUN}'s mouth, in the {RECORD_NOUN}). The type system cannot see it: the field is present, typed, and simply never read.
- Validate at the entry of data, never `as`-cast it — jsonb columns, LLM output, external payloads are parsed/validated (Zod, pydantic) where they enter; an `as` cast blinds `tsc` to the exact nullability mismatch that crashes at the first real row.

<!-- KEEP the next rule only if the roster has a project with its own SQL/migrations directory; drop it for a roster with no database. -->

- SQL lives in ONE place: `{MIGRATIONS_DIR}` migrations — no `.sql` file exists in any other project
- Surgical changes — every changed line traces to the task; don't refactor/rename/restructure working adjacent code. Always fix broken code you hit. Exception — dead code and unused deps: remove entirely.
- When removing code, delete end to end like it never existed
- Follow placement conventions: code placement per child `CLAUDE.md`; match existing naming/structure; no new dirs or patterns unless the task requires them.
- NO duplicatation: grep for the existing function/component/hook/type/util and import it; extract and call, never keep a near-copy.
- Right-size and finish: simplest thing that works; no speculative abstractions; complete, no stubs (`NotImplementedError`, lone `...`, deferred-TODO); import only manifest packages.

### Process

- NEVER edit code on `main`: worktree branches only, gitter-merged after the flight's gate passes; a change made on `main` by explicit command still gets its gate pass afterwards
- Only gitter WRITES git — commit/merge/checkout/branch/stash/reset/push and any other state-changing git are gitter-only for every agent; read-only git (status/diff/log/show/rev-parse) is open to all.
- NEVER commit broken code or merge before the gate passes
- Only the main-loop session writes permanent docs (`docs/agents/`, each project's `docs/`), under the `/quality:doc` Approval gate; `docs/epics/legal/` belongs to `/officer`; `docs/business/` to `/mentor` and `/marketer` (`marketing/`); `docs/facts/` — main loop only, solely on the user's explicit ruling
- Never install unvalidated libraries

<!-- KEEP the next rule only if the roster has a project that owns infra/orchestration (its directory is `{PROJECT}`); drop it for a roster with no such project. -->

- Infra ops go through the owning project's `Makefile` (`make -C {PROJECT}`) — never direct `{CONTAINER_RUNTIME} exec` / `{DB_CLI}` / `{CLOUD_CLI} {QUEUE}` calls
- Guarded files: PreToolUse hooks gate `.claude/**` + every `CLAUDE.md` (route: `/pcm`); the deny message carries the unlock steps
- Worktrees are costly: batch a session's related changes into one, and ask before creating one.

### Testing & Environment

- CI verifies, never debugs: reproduce and fix locally, then trigger CI.

### Meta

- **Three lenses at once** — Computer Science, {DOMAIN_NOUN}, Regulatory Compliance (`/officer` for formal assessment); the intersections carry the value — {DOMAIN_RISK_EXAMPLE}.
- **AskUserQuestion is the user's whole screen** — chat prose between dialogs never reaches them: context travels inside the question text; a clarification gets its answer in the next question's title, simpler and more concrete each round, never a rephrase.
- **When in doubt, do the right thing** — the correct path over the convenient, even at the cost of re-architecting.

## Model Selection

Tiers, effort, and delegation posture live in the applicable harness prompt's § Model Selection — never restated here.
