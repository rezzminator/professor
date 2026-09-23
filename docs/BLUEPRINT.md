# BLUEPRINT — The Philosophy

The discipline + character of the pipeline. Read this before installing it.

## Contents

- [The three-tier framework](#the-three-tier-framework)
- [The five load-bearing walls](#the-five-load-bearing-walls)
- [The non-negotiable rules baked into every install](#the-non-negotiable-rules-baked-into-every-install)
- [Pipeline architecture](#pipeline-architecture)
- [File layout (what you end up with after install)](#file-layout-what-you-end-up-with-after-install)
- [What you get out of the box](#what-you-get-out-of-the-box)
- [What you adapt vs. what you keep](#what-you-adapt-vs-what-you-keep)
- [Optional: Codex dual-runtime](#optional-codex-dual-runtime)
- [Staying current — the update mechanism](#staying-current--the-update-mechanism)
- [The smell test](#the-smell-test)

> **Personality is load-bearing.** Strip the Professor's voice and you have a Confluence wiki. Strip Professor's cross-disciplinary depth and the analysis becomes generic. The blueprint is a transplantable nervous system — characters, polymath Professor and all — refitted to your domain at install time. It drops into **any Claude Code project at any repo size**: structure is captured at install as a **roster** of 1..N projects, so a single-project repo (roster of one — first-class) and a multi-project repo get correctly-sized files from the same templates.

---

## The three-tier framework

Every command, agent, and rule sorts into one of three tiers:

| Tier | Description | What ships | What gets parameterized |
| ---------------------------- | ----------------------------------------------------------------------- | ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| **A — Universal archetypes** | Personalities that work in any domain. The voice IS the value. | Full character, voice, structure, signature traits, archetype identity | Domain-specific REFERENCES inside the character (the Professor's domain-specific analogies) |
| **B — Domain archetypes** | Roles every serious project needs, but content is heavily domain-shaped | Archetype skeleton: identity, voice, charter, mode list, doc structure | Regulation name, knowledge domain, user persona, market segment — filled at install via interview |
| **C — Pure mechanics** | Infrastructure agents and pipeline plumbing | Mechanics only — no character needed | Tech-specific commands (test runner, package manager, build tool) |

### The cast (Tier A — universal)

- **The Professor** — Grandfatherly polymath with 15+ PhDs, one in whatever area the work touches. Warm, precise, gently devastating. The orchestrator voice and root persona. Lives in `pfm/harness-prompts/`, composed per engine and selected by the Claude launch policy.
- **/pcm** — Professor Change Manager: edits the pipeline at the source. Surgery, not journaling. `/pcm audit [scope]` (`agents`, `commands`, `skills`, `pipeline`, `scripts`, `structure`, `cross-refs`, or `all`) walks the pipeline's own files against a checklist per scope; `/context-meter` audits the framework's own context budget.
- **/flights:{spec,orchestrate-nested,orchestrate-live,orchestrate-cross-harness,audit}, /dev** — pipeline mechanics; the harness supplies the Professor voice. `/reload` is the same tier but installs host-level (`~/.claude/commands/`, opt-in) from the self-contained `pfm` binary; chat control is the opt-in chat MCP server the same binary registers.

> The Tier A persona ships as ONE version: `professor.md` (the harness replacement) — lean voice plus the behavioral contract (concise delivery, the Verdict, the Analysis Protocol).

**Bundled commands (ship with the blueprint):**

- **the framework bus** — the framework repo's release flow publishes the blueprint; project installs are scaffolded once and adopt later template deltas by reviewed diff.
- **/flights:spec** — maps the area, grills the user round by round with a recommended answer per question until no technical or product gap is left, hands `flights-speccer` the decisions, and presents the index it wrote.
- **/flights:orchestrate-{nested,live,cross-harness}** — one manual in three containers: a `flights-orchestrator` sub-agent, the main chat running the flight itself, or chat seats on three engines as the executors.
- **/flights:audit** — the skeptic over a flight, running or landed: every claim checked against its artifact — `run.md`, git, the executor transcripts, the checks' own output — and an artifact it cannot read is a finding, never an absence.
- **/rnd** — project-scope RND lifecycle: opens, continues, verifies, and lands a research run, executing the run itself.
- **/tokens** — per-agent/per-workflow token spend attribution parsed from local transcripts, ranked by estimated cost.
- **/quality:doc** / **/quality:prompt** / **/quality:description** / **/quality:md-forlint** — the quality gates: reference-doc shape, prompt prose, the `description:` routing field, and markdown lint/format mechanics.
- **/quality:llm-codebase** — source-tree layout designed for agent maintainers: one directory per unit of change, a fixed file anatomy, grep-true names, façades for the cross-cutting calls, and the brief anchors a build hand reads; greenfield designs a tree, brownfield measures the existing one and writes the migration.
- **/quality:integration-suite** — a project's whole test suite designed, every tier's validity law and the live tier's lanes over one shared state: landscape derived from code, a research pass over neighbour projects and literature, crossings asserted from two sides, a machine-derived map gate, and the harness contract.
- **/audit:code-hygiene** / **/audit:security** — code-hygiene and security audit scopes. Code-hygiene additionally has a Sweep Mode (`code-hygiene sweep`) that promotes a report-only run to actively removing confirmed-dead code and unused dependencies, end-to-end behind QA.

**Machine-global skills (shipped under `templates/global/skills/`; its `sources.json` declares the source-fetched ones and the in-tree links):**

- **deep-rr** — in-tree research protocol under `workflows/deep-rr/`, linked by host installation.
- **ghostwriter** — captures a writer's mechanical fingerprint and generates in that voice.
- **vision-factory** — forge, validate, and stress-test a startup vision.

**Bundled skills (ship with the blueprint):**

- **legal** — an attributed reference shelf for the Professor and `/officer`: DPA and DPIA drafting, breach response, privacy notices, vendor due diligence, NDA/risk triage, and a pre-delivery self-check.

### The optional cast (Tier B — opt-in at install)

- **/officer** — compliance enforcer. Pick your regulation(s). (GDPR, HIPAA, FDA, SOC2, ISO 27001, MiFID, none.)
- **/mentor** — business advisor. Pick your market + jurisdiction.
- **/marketer** — visibility strategist. Pick your channels + language.

### The plumbing (Tier C — invisible)

- `gitter` — root agent; `tracer` (prose answers for a spec writer), `mapper` and `collector` (the two over the `codeprobe` skill's script), `flights-speccer` (writes a flight's task files), `flights-orchestrator` (runs them), `flights-mechanical-executor` (one task file each), `flights-lander` (gates the landing, one per project), `general-orchestrator` (cuts a batch of clear tasks and runs them), `general-mechanical-executor` (one task each), `reviewer`, `rr`, `sub-rr` (the digger the `rr` leads spawn), and `collector-rr` (fetches named web sources verbatim, no diggers) are machine-global originals under `templates/global/agents/`, linked by `pfm install`; `variants.json` beside them declares agents rendered from an original with overridden frontmatter and, under `replace`, body text swapped (`flights-smart-executor` = `flights-mechanical-executor` on opus, `general-smart-executor` = `general-mechanical-executor` on opus, `super-rr` = `rr` at medium effort with 6 diggers a round and 5 rounds, `heavy-rr` = `rr` at medium effort with 8 diggers a round and no round ceiling), which `pfm install` writes to its own generated directory and links the same way. Role-defined, not character-defined.
- `worktree.sh`, `alloc-ports.sh`, `dev.sh` — scripts.
- `pfm statusline` — native status bar with model, fleet counts, context, git, cost, spend, and rate limits. Wired in the host settings by `pfm install`.
- `.rumdl.toml` — the markdown policy: one config whose `[per-file-ignores]` table decides which rules each path category obeys (prompt, doc, public; generated and record paths excluded). Read by `/quality:md-forlint` and by the `format-md.sh` hook.
- `settings-global.json` — a handful of keys merged (never overwritten) into the adopter's own `~/.claude/settings.json`. Currently one key, `cleanupPeriodDays: 36500`, which turns off Claude Code's default 30-day auto-delete of session transcripts and orphaned git worktrees.
- Per-project testing manuals (`/{project}-testing-manual`) — the project's test law, read by the flights agents; a per-project specialist agent only where a project writes its own.

---

## The five load-bearing walls

Touch anything else, but leave these five alone. They are non-negotiable:

### 1. Only `gitter` touches git

The `gitter` agent is the **single git operator**. No other agent runs `git add`, `git commit`, `git merge`, `git push`. This isn't bureaucracy — it's safety:

- Centralizes destructive operations behind one well-tested code path.
- Prevents agents from racing each other for the merge.
- Makes "what got committed" auditable.

If an agent needs to commit, it asks gitter. Gitter has phases: SETUP, COMMIT, MERGE, PUSH, PULL, and TAG. The active main Codex chat may use the explicit-authority fallback only when the registered role is unavailable; subagents remain read-only.

### 2. QA gates the merge

The pipeline runs QA on the worktree branch BEFORE merging to main. Test failures block the merge. Then it runs **post-merge QA on main** to verify the merge didn't break anything. Zero tolerance for "pre-existing failures" — if a test was broken before your pipeline, your pipeline fixes it. Every pipeline leaves main cleaner than it found it.

### 3. Path variables, not hardcoded paths

Agents receive paths as variables:

| Variable | Purpose | Example |
| ------------ | ---------------------------------- | ---------------------------------------- |
| `$PIPELINE` | Pipeline name (kebab-case, unique) | `{some-feature}` |
| `$DOCS` | Pipeline docs from repo root | `docs/dev/tasks/{some-feature}` |
| `$DOCS_REL` | Pipeline docs from worktree | `../../../docs/dev/tasks/{some-feature}` |
| `$WORKTREE` | Worktree directory | `.worktrees/{some-feature}` |
| `$ARCHIVE` | Archive parent | `docs/dev/tasks/archive` |
| `$CDOCS` | Command-owned docs root | `docs/commands` |
| `$REFS` | Reference docs subdir | `references` |
| `$RESEARCH` | Research docs subdir | `research` |
| `$RESOURCES` | Static resources subdir | `resources` |

Agents NEVER hardcode `docs/dev/tasks/...` — they use what the brief that spawned them passes. Path conventions can change without rewriting every agent.

### 4. Worktree isolation per pipeline

Every code pipeline creates:

- A git branch: `pipeline/{name}`
- A worktree checkout: `.worktrees/{name}/` (full repo)
- A unique port allocation (whatever ports your stack needs)
- Pipeline docs: `docs/dev/tasks/{name}/`

This means you can run **multiple pipelines in parallel on the same machine** without port collisions or git state corruption. A flight that builds code is briefed with the worktree it works in, so every executor it dispatches lands in the same isolated checkout. When the pipeline completes, gitter merges to main, the worktree is removed, and the docs are archived.

### 5. Self-improvement at the source

When something goes wrong in the pipeline, you don't write a "lesson" file. You invoke `/pcm` (the change manager that owns the pipeline itself). It edits the actual agent definition or command instructions to prevent the bug class going forward. **Surgery at the source.** Pipeline files are meant to evolve.

---

## The non-negotiable rules baked into every install

These rules appear in `CLAUDE.md` and are referenced by every agent. They are the contract:

1. **No code on main except gitter merges.**
2. **Only gitter runs git commands.**
3. **Never commit broken code** — QA must pass first.
4. **Never merge before QA passes** — both pre-merge and post-merge.
5. **Never reuse pipeline names** — check `docs/dev/tasks/`, `docs/dev/tasks/archive/`, `.worktrees/` first.
6. **Never run destructive git commands** — no `--force`, no `reset --hard`, no `clean -fdx` without explicit user approval.
7. **Never swallow exceptions silently** — every catch logs the full traceback. Silent failures hide bugs.
8. **No mocking internal dependencies within 1 hop** — mock only external services (paid APIs, third-party SaaS, anything flaky and outside your trust boundary). Real DB, real queue, real internal services.
9. **All failing tests are blocking** — no "pre-existing failure" excuse.
10. **All infrastructure ops go through a single project-owned script** — never reach around it directly from agent code.

---

## Pipeline architecture

Work takes the lowest rung that fits (the fleet prompt's § Orchestration): direct, then `general-orchestrator` for a batch of clear tasks, then a **flight** — one spec directory, one orchestration, one landing — for large work whose solution is not in hand. A flight's pipeline:

```text
  /flights:spec          map the area · ask what the code cannot answer ·
                         hand the decisions to flights-speccer · present
                                     │
                                     ▼
  flights-speccer        $HOME/.local/state/pfm/flights/{project}/{flight}/ — index.md, one
                         self-contained task file per executor, shared
                         0-{topic}.md files
                                     │
                                     ▼
  the orchestration      one manual, three containers — the user picks one:
                         /flights:orchestrate-nested         a flights-orchestrator sub-agent
                         /flights:orchestrate-live           the main chat runs the manual itself
                         /flights:orchestrate-cross-harness  chat seats as the executors
                                     │
                                     ▼
  the run                every ready task dispatched together, one FRESH
                         executor per task file; each return verified against
                         the diff before run.md records it; SPEC-DRIFT and
                         FAILED go back to flights-speccer, never to a guess
                                     │
                                     ▼
  the landing (once)     one flights-lander per project: checks, one review of
                         the whole diff, adversarial tests, its own fixes ·
                         the standing checks watched printing · gitter
                         commits when the brief asked for a commit
                                     │
                                     ▼
  /flights:audit         at any time, by the user: run.md, git, the task files,
                         the executor transcripts, the checks' own output —
                         artifacts, never a report
```

The executor is `flights-mechanical-executor` or `flights-smart-executor`, picked by the task's rating, or a seat on another engine; it writes the code and its covering tests in the pattern of the project's testing manual, and one `flights-lander` per project closes the flight — checks, one review of the whole diff, adversarial tests, its own fixes. Specifying and running are two decisions: approving an index never starts a run, and the user picks the container.

Meta path: `/pcm {request}` → edits the agent definitions at the source.

---

## File layout (what you end up with after install)

```
your-project/
├── CLAUDE.md                          ← root project rules; the harness supplies the Professor persona
├── AGENTS.md                          ← (OPTIONAL) COMPILED from local CLAUDE.md by `pfm codex build` (Codex reads this by convention)
├── .rumdl.toml                        ← markdown policy per path category (`/quality:md-forlint`); the format hook reads it
├── .professor/
│   ├── VERSION                        ← installed blueprint version (e.g., vX.Y.Z)
│   ├── manifest.json                  ← interview answers (user-owned install record)
│   └── baseline.json                  ← per-local-file template hash + blueprint SHA pins (pfm-owned)
├── .claude/
│   ├── agents/                        ← root agents (gitter; tracer and the whole flights cast are machine-global)
│   ├── commands/                      ← /pcm, /pfm (the CLI guide), /context-meter, /dev, /audit:{code-hygiene,security}, /quality:{prompt,doc}, /rnd, /tokens + opt-in Tier B (`/flights:*` and `/reload` are NOT here — `pfm install` installs them host-level)
│   ├── scripts/                       ← worktree.sh, alloc-ports.sh, dev.sh, format-md.sh, checkpoint.sh, git-lock.sh, guard-stamp.sh, drain-wait.sh
│   ├── skills/                        ← bundled legal shelf + project source registry; machine-global skills live under templates/global/skills/ (its sources.json declares the fetched ones)
│   └── settings.json                  ← permissions, env vars, hooks (notify, formatter, statusline)
├── .codex/                            ← (OPTIONAL) pointer layer over .claude/ — never a restatement of it
│   ├── config.toml                    ← sandbox reach + the {CODEX_MODEL}/{CODEX_REASONING_EFFORT} pins
│   ├── rules/                         ← repo-law.rules — execpolicy door lock for non-gitter roles
│   ├── agents/                        ← GENERATED from every .claude/agents source, including gitter
│   └── skills/                        ← GENERATED by `pfm codex build` (symlinks + SKILL.md pointers) + hand-written keepers
├── {project-a}/                       ← first subproject (you name it)
│   ├── CLAUDE.md                      ← project-specific rules
│   └── .claude/agents/                ← project specialists, when the project writes any
├── {project-b}/                       ← second subproject
│   ├── CLAUDE.md
│   └── .claude/agents/
├── docs/
│   ├── agents/                        ← cross-project permanent docs (architecture, API, map, features)
│   ├── commands/{cmd}/                ← command-owned docs ($CDOCS root)
│   │   ├── references/                ← must-know
│   │   ├── research/                  ← looked-up material
│   │   └── resources/                 ← static assets
│   └── dev/
│       ├── tasks/{pipeline}/          ← temp pipeline docs
│       └── tasks/archive/             ← completed pipelines
└── .worktrees/                        ← git worktree checkouts (gitignored)
    ├── {pipeline}/                    ← per-pipeline checkout
    └── .ports                         ← port allocation registry

Scratch protocol state (flight specs, lane/timing artifacts, doctor captures)
lives outside the repo, at `/tmp/{project}/` (`{project}` = this repo's
directory name, leading dot stripped) — never under a repo-local `tmp/`.
A flight's own layout: `$HOME/.local/state/pfm/flights/{project}/{flight}/` — index.md, task
files, run.md, gate-{project}.md, audit.md.
```

For a single-project repo, drop the `{project-a}/`, `{project-b}/` layer — agents live in `.claude/agents/` only, no child CLAUDE.md files.

---

## What you get out of the box

A `.claude/` infrastructure — a **transplantable nervous system** — that turns Claude Code from "an AI that writes code when you ask" into **a self-disciplined engineering team with character**. Built by the Professor (the grandfatherly polymath behind the glass).

- **Worktree isolation** — every feature gets its own git worktree branch + a unique port allocation. Multiple parallel pipelines on the same repo without collisions.
- **A pipeline that refuses cowboy coding** — one task file per executor, every return verified against its diff, a `flights-lander` per project blocking bad code from reaching `main`.
- **One agent owns git** — only `gitter` runs `git add` / `commit` / `merge`. Centralized, auditable, safe.
- **Cross-disciplinary analysis** — the Professor brings 15+ PhDs to bear on architecture, design, and safety/correctness questions. The Analysis Protocol lives in the fleet prompt's shared head (`pfm/harness-prompts/share/head.md`), injected via `pfm` `claude.systemPrompt = "professor"`.
- **Self-improvement** — `/pcm` is the change manager that edits its own pipeline rules at the source.
- **Optional dual-runtime** — Codex (OpenAI) can mirror the Claude pipeline as a cheaper implementation layer. Same manuals, different runtime. Everything works without it.
- **Path conventions that scale** — `$DOCS`, `$WORKTREE`, `$CDOCS` so agents never hardcode paths.
- **Documentation discipline** — pipeline docs are temporary and archived; only the main-loop session writes to permanent project docs (a command-owned surface such as `docs/business/**` is written by its owning command), every write under the `/quality:doc` Approval gate.
- **Memory backup (opt-in)** — a `SessionEnd` hook auto-syncs Claude's persistent project memory to a private repo, so a machine wipe doesn't lose what Claude learned. Plain git, zero tokens. See `references/memory-backup.md`.

---

## What you adapt vs. what you keep

**Keep verbatim:**

- The `gitter` agent (with project list adjusted at install)
- The `worktree.sh` and `alloc-ports.sh` scripts (with port ranges adjusted)
- The flight lifecycle in the `/flights:*` commands — specify, orchestrate, land
- The path variable conventions
- The five load-bearing walls
- The non-negotiable rules
- **The character voices** — the Professor's grandfatherly precision, Professor's cross-disciplinary structure, the Tier B archetype identities

**Adapt at install (via the SETUP interview):**

- Project name + project list (your subprojects)
- Tech stack descriptions in each project's `CLAUDE.md`
- Test / lint / typecheck / build commands the agents run
- Port ranges (whatever's free on your machine)
- The project roster (your 1..N projects — directories, stacks, package managers, test runners, ports)
- Tier B opt-ins (which optional archetypes you enable — regulation, knowledge domain, user persona, market segment)
- The character name (default: "Professor") if you want a different persona

See `SETUP.md` for the install interview and adaptation guidance.

---

## Optional: Codex dual-runtime

Professor's nervous system can optionally span **two AI runtimes**: Claude Code (Anthropic) and Codex (OpenAI). Everything works with Claude alone — Codex adds a cheaper implementation layer.

**How it works:**

- The project's local `CLAUDE.md` and `.claude/` files are the source of truth for its command manuals, agent definitions, scripts, and shared skills.
- `.codex/` is a **pointer layer** over those local sources, never a restatement of them. `pfm codex build` compiles `AGENTS.md`, command and skill pointers, `.codex/agents/*.toml`, and the managed MCP fence; `pfm codex check` reports drift without writing. Generated-marker ownership keeps foreign files at managed paths as named conflicts.
- `scripts/codex-sync.sh` keeps the mirror deterministic: its hooks mark a local source edit dirty, then run `pfm codex build` and `pfm codex check` before the turn ends. A mirror that fails to compile blocks the turn rather than shipping broken.
- `.codex/agents/*.toml` is the active Codex role registry, and `spawn_agent` selects a registered role. Registry changes take effect in a new or reloaded Codex session; `.codex/rules/repo-law.rules` is the execpolicy door lock.
- Claude and Codex mirror the same Professor contract. The pointer layer translates mechanics, never identity or protocol. The generated `gitter.toml` is the sole Git-writing Codex role; all other Codex agents keep read-only Git.

**Division of labor:**

| Task | Runtime | Why |
| -------------------------------- | ------------------- | -------------------------------------------------------------------------------- |
| Planning, architecture, research | Claude | Judgment-heavy, low token volume |
| Heavy implementation | Codex | Cheaper per token |
| QA / adversarial tests | Claude | Codex shouldn't grade itself |
| Git operations | Registered `gitter` | Claude and Codex expose the same sole-writer role; every other role is read-only |

**Opting in:** the install interview asks its optional Codex question. If yes, it creates `.codex/` (`config.toml`, `rules/repo-law.rules`, and the generated registries), runs `pfm codex build` followed by `pfm codex check`, and wires `scripts/codex-sync.sh` so later local source edits recompile before the turn ends. If no, the entire layer is skipped. No pipeline operation requires Codex.

See `templates/project/codex/README.md` for the full integration guide.

---

## Staying current — the update mechanism

The blueprint evolves through semver git tags. Each tier has one source of truth and one update path:

| Tier | Truth | Staying current |
| ----------------------------------------------------------- | ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Machine-global commands, agents, and skills | Blueprint originals | Symlink-live. `pfm update` advances the recorded tagged clone, rebuilds the binary, runs `pfm install --yes`, and refreshes registrations. |
| Project files (`CLAUDE.md`, `.claude/**`, docs, scripts) | **The local file, full stop** | `pfm init` scaffolds it once. `pfm update check` reports upstream template deltas for review and hand application; pfm never rewrites it during update. |
| Engine mirrors (`AGENTS.md`, `.codex/**`, OpenCode outputs) | Generated from local project files | Never edit by hand. Run the owning compiler, including `pfm codex build                                                                                 | check`, after changing local sources. |

`pfm init` records each deployed local-to-template mapping in `.professor/baseline.json`. The pin hashes the template bytes with tokens intact and records the blueprint SHA; local token filling and later customization do not change that provenance. `.professor/manifest.json` remains the user-owned interview record.

### Review and adopt project-template changes

0. An install without a baseline (it predates `pfm init`) runs `pfm update adopt [--at <ref>]` once to pin its existing local files; `--at` anchors the pins at the blueprint ref it last synced from.
1. Run `pfm update check` inside the project. It reads the blueprint, baseline pins, and local paths, then reports without writing. Bare `pfm update` performs the machine update first and appends the same project report when it finds a baseline.
2. Review every non-current status:
   - `UPDATED` — inspect the printed `git -C <blueprint> diff <pinned>..HEAD -- templates/<template>` command and hand-apply only the parts that belong locally.
   - `NEW` — adopt it only if useful, then create its mapping with `pfm update pin --template <template> <local>`; `pfm update ignore <template>...` keeps a template the project will never take out of every later report.
   - `GONE-UPSTREAM` — keep the local file as yours or delete it, then `pfm update drop <local>`.
   - `LOCAL-DELETED` — restore the local file or drop its pin.
3. After applying an `UPDATED` file, advance that one baseline with `pfm update pin <local>`. Use `--all` only after reviewing and applying every reported updated file.
4. Re-run `pfm update check`; it is clean only when nothing needs review. Rebuild enabled engine mirrors from the resulting local sources.

The report is the update UI. Project updates never regenerate local files, replay the interview, apply template changes automatically, or perform a three-way merge. See `SETUP.md` § "Staying current" for the command-level workflow.

---

## The smell test

**Could a neuropsychology lab, a tabletop RPG studio, and a SCADA controls team all read this blueprint and see _their version of the Professor and the audit cast_ — same archetypes, different content?**

If yes, the blueprint is right. If anyone has to delete personality before using it, the blueprint failed.

The mechanics survive every stack. The characters' voices survive every domain. Personality is not decoration — it's load-bearing. If you find yourself stripping voice to "make it generic," stop and parameterize the content instead.
