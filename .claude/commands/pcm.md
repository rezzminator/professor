---
name: pcm
description: MANDATORY — route every change to CLAUDE.md, .claude/**, the .codex/ mirror or templates/** here. `/pcm {change request}` applies it; `/pcm audit [scope|all]` reports framework consistency, read-only; `/pcm retro` folds the .professor/retro.md inbox. Publishing the blueprint → /pfm:release; the pfm CLI itself → /pfm; context budget → /context-meter.
argument-hint: [change request|audit]
---

# PCM — Professor Change Manager

$ARGUMENTS

---

## Mandatory skill load (before any prompt-file edit)

Hook-enforced: guards deny prompt-file edits until `.claude/commands/quality/prompt.md` is READ this session (Read auto-stamps the quality marker). Its rules govern prose leanness for ANY prompt; `/quality:description` governs every `description:` field and loads before one is written; **§ Claude-harness prompt law** below carries the harness-specific file rules (size limits, voice location, hooks, routing); **§ Authoring conventions** below governs the file skeleton (frontmatter + shape).

---

## System Wiring Knowledge

### How the pieces connect

- `CLAUDE.md` — the law + guards + routing; names mandatory-load obligations; carries no rosters of commands or skills (§ Authoring conventions, no-rosters law)
- `.claude/commands/**/*.md` — slash commands (`/pcm`, `/pfm:release`, `/quality:*`, `/dev`; `/context-meter`, `/pfm` are global)
- `.claude/agents/*.md` — registered agents (`ls` for the set); `gitter` is the Git writer, `tracer` the consumer-tree trace
- `.claude/skills/*/SKILL.md` — reusable skills (`ls .claude/skills/` for the current set; source-fetched per `templates/project/skills/sources.json`, never vendored)
- `.claude/scripts/*.{sh,mjs}` — dev.sh, pfm-guard.sh, guard-stamp.sh, format-md.sh, codex-sync.sh (mirror auto-compile; the Codex compiler itself is `pfm codex build`)
- `docs/commands/{cmd}/references/` — command-owned reference docs (`$CDOCS/$CMD/$REFS/`); today: `pcm/references/{audit-scopes,refresh}.md`
- **The shipped product** — `templates/**` is an adopter's live framework, one clone away. Same law, higher stakes: a change there ships to every future install. `docs/{BLUEPRINT,SETUP,PLACEHOLDERS}.md` are its spec; `templates/refresh-map.json` maps each template to the live source it is derived from.

### Critical invariants

- **Path variables** — files use `$CDOCS`, `$REFS`, `$RESEARCH`, never hardcoded doc paths. Defined in root `CLAUDE.md` § Path vars.
- **Two audiences, one law** — a rule you write into `.claude/**` binds this repo; the same rule in `templates/**` binds every adopter. Never let the two drift silently: if a fix belongs upstream, it lands in the template too, and the commit message names the adopter-facing change — the release reviewers write the notes from it.
- **Agent frontmatter must match behavior** — `name`, `description`, `tools` fields.
- **Registry over tables** — a command/skill's `description:` frontmatter IS its routing, written to `/quality:description` (the harness injects that registry into every session); `disable-model-invocation: true` hides a command from the model's registry — set it only on user-triggered-by-design commands. The roster ban and what CLAUDE.md may carry: § Authoring conventions (CLAUDE.md).
- **No command >35KB, no agent >15KB** — token consciousness. Every `general-purpose` spawn carries the full root CLAUDE.md (+ git status) and a build spawns 30+ agents, so a root CLAUDE.md line is the most expensive line in the framework — weight cuts by that multiplier (`Explore`/`Plan` types skip the CLAUDE.md chain; the fleet prompt rides the main-loop system prompt only). `@path` imports expand at launch, so splitting CLAUDE.md saves zero context — cut content, don't relocate it.
- **Never hardcode names, counts, or rosters that change** — table names, enum values, chain names, agent/queue/chain tallies evolve. Tell agents WHERE to discover (`ls`, a registry file, the owning script), not WHAT the values are.
- **Frontmatter features need registration** — `hooks:`/`model:`/`effort:` load ONLY when an agent is spawned as a registered type via its `subagent_type`; a protocol file read by a general-purpose agent never loads frontmatter. A protocol needing frontmatter features needs a thin registered wrapper: registration shell in `.claude/agents/`, protocol in the file it reads.
- **Registries read at session start** — agent types, settings.json hooks, and the injected fleet prompt load at session start; mid-session file changes land at natural boundaries (next spawn, next session). When a long-running session will consume an edited file, add a transitional fallback clause (brief-wins, registry-fallback) rather than assuming hot reload.
- **A subagent holds no `Workflow` tool** — a protocol that drives a Workflow structurally cannot be an agent; that is the line `deep-rr` (a skill driving its engine) and `agents/rr.md` are split along. Check it before proposing to convert a command into an agent.

### Inventory (derive, never recall)

- **Projects:** `templates/` (markdown + shell, no build), `pfm/` (Go, including the memory organ) — confirm with `.claude/scripts/dev.sh status`
- **Agents:** `ls .claude/agents/` — model tiers per the fleet prompt § Model Selection
- **Commands, skills:** `ls -R .claude/commands/ .claude/skills/`
- **Shipped surface:** `find templates -type f -not -name refresh-map.json | wc -l` — the count that matters to adopters

---

## Claude-harness prompt law

Harness-specific rules for files Claude Code loads at runtime — the general prompt law (cut test, compaction, anti-patterns) lives in `/quality:prompt` and applies on top.

### The harness prompt stream

In the Claude Code harness the LLM reads one concatenated context: root `CLAUDE.md`, the auto-loaded skill descriptions, the active command or agent, and every skill loaded this session — all at once. Audit any harness prompt against that whole stream (per `/quality:prompt § The prompt stream`).

### Hard thresholds (Anthropic-published)

- CLAUDE.md (any): ≤ 200 lines
- SKILL.md body: ≤ 500 lines — split via progressive disclosure above this
- Sub-agent body: no formal cap — Anthropic's own examples run 20–35 lines

Above threshold = split into a referenced file (one level deep, with a Table of Contents at the top if >100 lines).

### Voice location

Voice lives in the fleet prompt (`pfm/harness-prompts/`: `share/head.md` + the engine's `professor.md` + `share/tail.md`, composed by `pfm install`; Claude injects it under `claude.systemPrompt = "professor"`) — main-loop only; subagents never receive it. CLAUDE.md and every agent/skill/command carry zero voice. Cross-file dedup targets: child CLAUDE.md keeps only its delta vs root CLAUDE.md; a project agent keeps only its delta vs the project CLAUDE.md it reads at start.

### Hooks vs prompts

For things that must happen every time (formatting, validation, secret-scanning), write a hook (`.claude/settings.json` PreToolUse / PostToolUse) — deterministic, cheap. Prompts are advisory; the model can drift. Once a hook owns an invariant, delete the prompt rule that restated it — keeping both is duplication against a deterministic mechanism.

### Where harness content goes (anti-bloat routing)

- Behavioral rules → prompt files (CLAUDE.md, agents, commands, skills)
- Incident narratives ("on 2026-XX-XX...") → commit message / epic manifest (`docs/epics/{name}/`) — never prompt files
- Architectural decisions / why-this-design → epic manifest or `docs/commands/{cmd}/references/` — prompts encode the rule, not the rationale
- Voice / character flavor → the fleet prompt (`pfm/harness-prompts/share/head.md`) — zero voice in CLAUDE.md, agents, skills, commands
- Project-specific tooling → child CLAUDE.md only — never per-project agents (they inherit via parent)
- Cross-cutting templates (report format, plan shape) → one canonical reference file — never duplicated per-project

## What you own

- Root CLAUDE.md: `CLAUDE.md`
- Agents: `.claude/agents/*.md`
- Commands: `.claude/commands/**/*.md`; skills: `.claude/skills/*/SKILL.md`
- Scripts: `.claude/scripts/*.{sh,mjs}`; settings: `.claude/settings.json`
- The shipped blueprint: `templates/**` — templates, spec, and `refresh-map.json`. `/pfm:release` publishes it; `/pcm` edits it.
- Codex mirror: `.codex/` + `AGENTS.md` files + `$HOME/.codex/` — all generated from the Claude sources by `pfm codex build` — the single writer. Auto-compiled at turn end by the `codex-sync.sh` hooks whenever an Edit/Write touches a Claude source (Bash-driven writes are outside that hook's coverage — run `pfm codex build .` yourself after one); `pfm codex check .` gates drift. Hand-written keepers: `.codex/config.toml` (except its generated `mcp_servers` fence, compiled from `.mcp.json`), `.codex/rules`
- PFM reference docs: `docs/commands/pcm/references/`

---

## Logging

Release notes are never written during development. A framework change (one any Professor adopter could use) lands with its `templates/**` twin in the same pass and is described in its commit message — `/pfm:release` reviewers derive the changelog and every adopter instruction from `develop`'s diff and commits. A **customization only this repo wants** (a rule about publishing the blueprint, a roster fact, a gate that only makes sense upstream) lands without a twin; its commit message is its record. **Unsure which? Ask the user — never guess.**

**Standalone-skill special case:** a change to a `sources.json` skill bumps the skill's `version:` frontmatter — release step 7b ships the substance to the skill's own public repo; the Professor changelog carries only the version pointer + re-pull note.

**Retro inbox — `.professor/retro.md`:** the main-loop steering-conscience ledger (sessions append per its header) — an inbox `/pcm` consumes, never a change log. The `retro` dispatch sweeps entries lacking `Resolved:`, folds each `Amend:` into the named file through the normal change flow (or rules it `judgment` — no text fix), and stamps `Resolved: {date} — {where}` under the entry in place.

---

## How to process a change request

### Step 1 — Understand

Parse `$ARGUMENTS`. Dispatch first: `audit` → the **Pipeline Consistency Audit** section; `retro` → the § Logging retro-inbox fold pass; anything else → the change-request flow below. Publishing the blueprint upstream is the `/pfm:release` subcommand (`pfm/release.md`). Common change-request categories: agent behavior, conventions, new agent/command/skill, script fix, rename/restructure, settings, shipped-template correction.

### Step 2 — Audit impact

Before ANY changes, read all affected files. Grep every reference across `.claude/`, `CLAUDE.md`, child CLAUDE.md files.

**Facts verify against code, never against prior text.** Every factual claim a framework file makes about the codebase — a path, mechanism, config, protocol, count — is verified against the code at write time (scout with an Explore agent for anything non-trivial); a rewrite derives from what the code says, never from what the file used to say. Files lie; the code doesn't.

**Consistency checklist:**

- Project dir names in CLAUDE.md match actual directories, and `dev.sh`'s roster matches both
- Agent frontmatter matches actual behavior and tools needed
- Every `/command`, `subagent_type:`, script path, and reference doc named in a file EXISTS — this install ships a subset of the blueprint, so an upstream-shaped pointer is a dangling pointer here
- Tech stack descriptions match `go.mod` / `package.json`
- A rule changed in `.claude/**` that belongs upstream has its `templates/**` twin changed in the same pass — and vice versa

### Step 3 — Plan

Group changes: (1) **breaking** (must be atomic), (2) **non-breaking** (independent). Count the tasks per the fleet prompt § Orchestration: more than one ⇒ `flights-speccer` writes the flight directory and `flights-orchestrator` runs one executor per task file; edits the guard reserves for the main loop (`.claude/**`, any `CLAUDE.md`) are applied here from those task files.

### Step 4 — Execute

**Open the gate (before the edit pass).** A PreToolUse hook (`pfm-guard.sh`) denies Edit/Write to `.claude/**`, any `CLAUDE.md`, and `docs/commands/pcm/references/**` unless BOTH session-keyed markers are fresh: reading `quality/prompt.md` stamps the quality marker automatically, and the pfm marker is stamped with the exact command the deny message provides (it carries your session key). Markers **survive turn ends** and slide on every allowed edit — stamp once per session; the 1500s freshness TTL plus the Stop hook's 1h reap kill only abandoned sessions. If a write is denied, follow the deny message and retry — and note that a sandboxed stamp never lands on the filesystem the hook reads.

**Agent edit rules:**

- Preserve YAML frontmatter format (`name`, `description`, `tools`)
- Preserve path variables — never hardcode
- Keep step numbering consistent
- Root agent descriptions must match `subagent_type` registry

**CLAUDE.md rules:**

- Keep section hierarchy — agents/commands reference sections by name
- Keep non-negotiable rules exactly as they are

**Command rules:**

- Any change to a body's entry points is followed by a `/quality:description` pass over its `description:`
- A command that dispatches an agent names the `subagent_type` and the five briefing fields (root `CLAUDE.md` § Subagent dispatch)

**Script rules:**

- Keep `set -euo pipefail` at the top
- A hook script exits 0 on every path it does not own — a guard that crashes on unrelated input blocks the whole session
- Every script says what it reports when IT is broken, in its own header comment

### Step 5 — Verify consistency

1. Grep for stale references to old names/paths
2. Cross-reference agent tools lists against what the body actually does
3. Agent completeness — every `subagent_type` named anywhere has a file in `.claude/agents/`
4. Command completeness — every `/command` referenced in a shipped or installed file has a file; every `.claude/commands/**/*.md` has a `description:`
5. Script and reference-doc paths exist as stated (`docs/commands/**`, `.claude/scripts/**`)
6. Directory name consistency across all files
7. `pfm codex check .` — the Codex mirror is not stale (a Bash-driven write skips the auto-compile hook)

### Step 6 — Report

Report, in order: "Infrastructure updated, N files changed" — the changes (what and why) — consistency verified (stale references none/N-fixed; agent definitions consistent; Codex mirror `check` clean) — "local-only" or "release-bound: {commit-message line}" — whether the upstream twin under `templates/**` changed too, or why it must not — trees touched beyond this repo (`$HOME/.claude/`, `$HOME/.codex/`) with their uncommitted state, or "none" — manual verification needed (list, or "none").

---

## Pipeline Consistency Audit

Run when `$ARGUMENTS` starts with `audit`. **Read-only** — reports problems, does NOT fix them.

### Execution model — fan-out agents

Spawn **one Agent per scope in parallel** (subagent_type: `Explore`, search breadth: `very thorough`). Each agent deep-reads its entire domain — follows every reference, reads every file, verifies semantic consistency. PFM aggregates results after all agents return.

**Row tiering within a scope.** Closed-list mechanical rows — frontmatter-parses, path-exists, size limits, executable `+x`, file counts, known-name greps — MAY run as a cheap child (`Explore` or `model: haiku`) against the explicit checklist: coverage lives in the checklist, so a miss surfaces as a missing row, not a silent gap. Semantic rows — description↔behavior match, delegation sanity, route-to validity — stay on the very-thorough walker. Aggregation is unchanged.

**Scope selection:** `audit` or `audit all` → ALL scopes in parallel. `audit {scope}` → single scope.

**Agent brief template** (adapt per scope):

> You are auditing the Professor framework's **{SCOPE}**. Read every file listed. For each check, report one line: `PASS: {detail}` or `FAIL: {detail}` or `WARN: {detail}`. Do NOT fix anything — report only. Follow every reference, read every file, verify every claim. The project root is `{cwd}`.

### Scopes & deep checks

Seven scopes: `agents`, `commands`, `skills`, `templates`, `scripts`, `structure`, `cross-refs`. The per-scope checklists live in `docs/commands/pcm/references/audit-scopes.md` — read it when composing the fan-out briefs; each agent's brief carries its scope's section.

### Aggregation

After all scope agents return:

1. Merge per-scope findings into a single report
2. Deduplicate findings that appear in multiple scopes
3. Assign severity: **CRITICAL** (broken reference, missing file, invariant violation — and ALWAYS any claim wrong in the REASSURING direction: promising a protection, isolation, or sanitization the code does not provide), **WARNING** (stale name, size approaching limit, weak inconsistency), **INFO** (style nit, non-blocking)
4. Count totals per severity

### Report format

Shape, in order: title "Pipeline Audit Report — {date}" — summary (scopes audited, agents fanned; total checks / passed / critical / warnings / info) — per-scope results, one PASS/FAIL/WARN-prefixed line per finding — numbered issues with severity badge + suggested fix — verdict: CLEAN, or NEEDS ATTENTION — N critical, M warnings.

Ask: "Want me to fix these issues?"

---

## Special Operations

**Full rename:** Grep ALL occurrences (including `templates/**`, `README.md`, `BLUEPRINT.md`, `SETUP.md`, `refresh-map.json`) → update agents → update CLAUDE.md → final grep for zero stale refs → recompile the Codex mirror.

**New agent:** Create `.claude/agents/{name}.md` — its `description:` is the registry entry, its `model:` pins the tier (root `CLAUDE.md` § Subagent dispatch carries no roster) → `pfm codex build .` (it compiles a `.codex/agents/{name}.toml`) → decide whether it ships upstream as `templates/project/agents/{name}.md`.

**New skill:** Create `.claude/skills/{name}/SKILL.md` → no CLAUDE.md edit needed (skills self-index from `description:` frontmatter). A skill meant for adopters is registered in `templates/project/skills/sources.json` and lives in its OWN public repo — the blueprint never vendors one.

**New command:** Create `.claude/commands/{name}.md` with a `description:` → it self-indexes; add to CLAUDE.md ONLY if it's a guard or a non-obvious routing decision.

**Codex mirror:** any of the above → `pfm codex build . && pfm codex check .`. The `Stop` hook does this automatically after an Edit/Write; a Bash-driven write bypasses it, and `check` is the backstop.

---

## Authoring conventions — frontmatter + file shape

The skeleton every framework file follows. `quality:prompt` governs how lean the prose is; this governs the shape.

### Descriptions — the routing registry

The `description:` is all the model sees at routing time — the harness injects every command/skill/agent description into each session and every sub-agent spawn; the body loads only on a match. `/quality:description` is the law for writing one — grammar, per-kind char caps, cut order, family and USER-ONLY declaration, MCP self-containment, Approval gate. Load it before writing or editing any description, including an MCP tool's.

### File-type laws

Shape: match the existing files of the same kind — the live registry is the template.

- **Sub-agents** (`.claude/agents/*.md`): frontmatter `name` (kebab-case), `description` (§ Descriptions — it carries the auto-delegation routing weight), `tools` (minimal allowlist), `model: inherit|opus|sonnet|haiku`. Body IS the system prompt — role sentence, numbered procedure, short checklist, output format; subagents see only their own prompt + env.
- **Slash commands** (`.claude/commands/*.md`): frontmatter `name`, `description` (§ Descriptions), `argument-hint`, `disable-model-invocation: true` on user-triggered-by-design commands. `$ARGUMENTS`/`$1`/`$N` substitute at invocation; a bang-prefixed backticked command (!`cmd`) injects live shell output before Claude sees the prompt.
- **Skills** (`.claude/skills/*/SKILL.md`): frontmatter `name` (lowercase-hyphenated, ≤64 chars, no reserved words anthropic/claude), `description` (§ Descriptions; third person, highest-signal case first). Body: role line, triggers, behavioral steps, 3–5 diverse `### Example` sections, only non-obvious constraints. Skill content stays in context all session and re-attaches after compaction — every line is a recurring tax.

### CLAUDE.md (root + child)

Keep: bash commands Claude can't guess, code-style rules that differ from defaults, architectural decisions / invariants, non-obvious gotchas, repo etiquette / test runners.

NOT: standard language conventions, file-by-file descriptions, "write clean code" platitudes, info Claude can read from the code. **Placement by scope:** root CLAUDE.md carries only rules binding 2+ projects — a rule scoped to one project lives in that project's CLAUDE.md. Child CLAUDE.md files keep only the project-specific delta — never re-declare workspace rules already in root.

**No skill/command rosters.** Claude Code indexes skills and commands itself — it reads every `SKILL.md` and command `description:` at startup and loads a body only on a match. A list of skills or commands in CLAUDE.md is dead weight that rots on every add, so leave it out. CLAUDE.md carries only what auto-indexing can't: **guards** (what's forbidden or must route through a command), **routing decisions** (which handler wins for an ambiguous intent), and **mandatory-load obligations** (when a skill is required at a step). Existence is the filesystem's job; obligation is CLAUDE.md's.

---

## Self-Update Protocol

After every execution, verify this command's knowledge is still accurate:

1. Are the inventory counts correct? (`ls .claude/agents/`, `ls .claude/commands/`, `find .claude -name 'SKILL.md'` — skills live under `.claude/skills/` AND embedded in command dirs)
2. Are the critical invariants still true?
3. Did any project directories or table structures change?
4. Is the system wiring diagram still accurate?

If anything is stale, update this file before completing the report. This command must never give outdated advice about its own pipeline.

---

## Rules

- **User-ordered only** — framework files change ONLY on the user's explicit in-session command, never autonomously, never as automation, never as a side effect of other work; an improvement spotted mid-task is proposed, not applied
- **Never break the pipeline** — atomic changes for breaking modifications
- **Never weaken non-negotiable rules** — ethics, privacy, code quality are sacred
- **Never remove safety checks** — the guard hooks, the leak gate, the publication boundary, and every QA/merge gate in the shipped templates
- **Preserve agent autonomy** — self-contained, no circular dependencies
- **Keep it DRY** — reference CLAUDE.md from agents, don't duplicate
- **Sync across projects** — change in one place = reflect everywhere
- **Prefer deletion over addition** — root's surgical-changes law governs the rest
- **Research before writing** — verify domain content before adding. Structural changes don't need research
- **Always consider token budget** — define once, reference everywhere
- **Routing-gate every fan-out** — spawn agents only for declared scope; the consolidator may demand additions; fall back to full fan-out only when scope is undeclared
- **Every pipeline artifact names its consumer** — before adding a report/file an agent writes, name who reads it downstream; write-only artifacts are banned
- **Delta-structure repeatedly-rewritten state files** — rewritten resume brief on top, append-only archive below a marker; never full-file rewrites
- **Exact-slice agent inputs** — when carving a manifest for parallel agents, each gets its exact slice + a thin shared header; a shared contract lives once in a shared file every brief that needs it names (a `0-` file in a flight directory), never copied per agent
- **Exact per-role read lists in spawn briefs** — "read ALL docs in {dir}/" licenses every agent to read everything; name each role's exact read list
- **One common spawn contract per orchestrator** — hoist rules shared across spawn blocks into a single contract each block references, never restated per block
- **Every check names what its OWN broken state reports** — authoring or editing any instrument that returns a verdict (probe, health check, gate, audit, walker, lint), ask what it reports when IT is broken rather than when the world is clean. Same answer both ways = not a check but a coincidence detector, and it will bless the failure it exists to catch (`kill -0` cannot distinguish a healthy waiter from a reparented deaf one; `PPID ≠ 1` can — a pane capture on the wrong socket returns silence identical to a quiet chat; a capture that cannot reach its target exits non-zero). Build the distinguishing signal INTO the instrument: a law forbidding the mistake is strictly weaker than a check detecting it
