---
name: pcm
description: MANDATORY — route every framework or process-file change here; owns CLAUDE.md, .claude/** and the .codex/ mirror. `audit [scope|all]` reports pipeline consistency, read-only; `retro` folds the .professor/retro.md inbox. Not for the pfm CLI or `pfm update` → /pfm.
argument-hint: "[change request|audit [scope|all]|retro]"
---

# PCM — Professor Change Manager

$ARGUMENTS

---

## Mandatory skill load (before any prompt-file edit)

Hook-enforced: guards deny prompt-file edits until `~/.claude/commands/quality/prompt.md` (machine-global `/quality:prompt`) is READ this session (Read auto-stamps the quality marker). Its rules govern prose leanness for ANY prompt; `/quality:description` governs every `description:` field and loads before one is written; **§ Claude-harness prompt law** below carries the harness-specific file rules (size limits, voice location, hooks, routing); **§ Authoring conventions** below governs the file skeleton (frontmatter + shape).

---

## System Wiring Knowledge

### How the pieces connect

- `CLAUDE.md` — request routing + guards; routes non-obvious requests to commands, names mandatory-load obligations; carries no rosters (§ Authoring conventions, no-rosters law)
- `.claude/commands/*.md` — project slash commands (/wave:orchestrator, /pcm, …); machine-global commands (`/wave:builder`, `/quality:*`, `/context-meter`, `/pfm`) live in `~/.claude/commands/`, symlinked to the blueprint clone by `pfm install`
- `.claude/agents/*.md` — root pipeline agents (gitter) + {proj}-qa wrappers, one per project (registered QA gates that read the child protocol and carry the test-output filter hook)
- `.claude/skills/*/SKILL.md` — reusable skills (`ls .claude/skills/` for the current set)
- `.claude/scripts/*.{sh,mjs}` — worktree.sh, alloc-ports.sh, dev.sh, pfm-guard.sh + guard-stamp.sh (the framework-edit gate), codex-sync.sh (the `pfm codex` hook bridge)
- `{project}/.claude/agents/*.md` — child project agents; `{project}/CLAUDE.md` — child project conventions. A `{project}` held as a git submodule lands its commits in the child repo, and the root repo pins a pointer (gitter-owned)
- `docs/commands/{cmd}/references/` — command-owned reference docs ($CDOCS/$CMD/$REFS/); `docs/agents/` — main-loop-owned cross-project reference clusters (`api/`, `architecture/`, `map/`, `features/`) + `standards.md`, `graph/`; `docs/facts/` — user-ruled system facts (main-loop-written, on explicit ruling only)

### Critical invariants

- **Path variables** — agents use `$DOCS`, `$WORKTREE`, `$CDOCS`, `$REFS`, never hardcoded paths; the wave command that spawns them (`/wave:builder`, `/wave:live`) defines them in the brief.
- **Pipeline flow lives in the wave commands** (`/wave:refine` → `/wave:orchestrator` → `/wave:builder` → `/wave:walker`; `/wave:live` for the direct-to-main path) — CLAUDE.md just redirects. Don't duplicate.
- **Agent frontmatter must match behavior** — `name`, `description`, `tools` fields.
- **Registry over tables** — a command/skill's `description:` frontmatter IS its routing, written to `/quality:description` (the harness injects that registry into every session); `disable-model-invocation: true` hides a command from the model's registry — set it only on user-triggered-by-design commands. The roster ban and what CLAUDE.md may carry: § Authoring conventions (CLAUDE.md).
- **No command >35KB, no agent >15KB** — token consciousness. Every `general-purpose` spawn carries the full root CLAUDE.md (+ git status) and a build spawns 30+ agents, so a root CLAUDE.md line is the most expensive line in the framework — weight cuts by that multiplier (`Explore`/`Plan` types skip the CLAUDE.md chain; the fleet prompt rides the main-loop system prompt only). `@path` imports expand at launch, so splitting CLAUDE.md saves zero context — cut content, don't relocate it.
- **Never hardcode names, counts, or rosters that change** — table names, enum values, chain names, agent/queue/chain tallies evolve. Tell agents WHERE to discover (`ls`, a registry file, the owning script), not WHAT the values are.
- **Frontmatter features need registration** — `hooks:`/`model:`/`effort:` load ONLY when an agent is spawned as a registered type via its `subagent_type`; a protocol file read by a general-purpose agent never loads frontmatter. A child agent needing frontmatter features needs a thin root wrapper (the `{proj}-qa` pattern: registration shell at root, protocol stays in the child file).
- **Registries read at session start** — agent types, settings.json hooks, and the injected fleet prompt load at session start; mid-session file changes land at natural boundaries (next spawn, next pipeline, next session). When a long-running session will consume an edited orchestrator file, add a transitional fallback clause (brief-wins, registry-fallback) rather than assuming hot reload.
- **Workflow scripts are schedulers** — workflow sub-agents carry NO Agent tool (no nesting) and no Skill tool; a saved workflow script must call every role directly via `agent()` — `agentType` resolves registered types (frontmatter model/hooks intact). A script's flow graph is a declared copy of its command file — update both in the same change. **One-level nesting only** (`workflow()` inside a child throws): when a workflow can't be nested at a call site, that site inlines the same `agent()` fan-out as a second declared copy. Sync set today: `doc-approval.md` ↔ `quality/doc.md` § Approval.

### Inventory (derive, never recall)

<!-- INSTALL: this section is derive-only by design — no fixed counts to fill in. The bash commands below run against the actual roster/filesystem every time, so a single-project install and a ten-project install both get correct answers from the same text. -->

- **Projects:** derive with `ls -d {project}*/`; each child CLAUDE.md § Quick Start names its package manager
- **Agents:** enumerate with `ls .claude/agents/ {project}/.claude/agents/` — every agent is registered at root on the `{proj}-{role}` convention (`{proj}-qa`, `{proj}-developer`, …), plus the project-neutral `gitter`; the machine-global cast (`architect`, `reviewer`, `tracer`, `scheduler`, `rr`) lives in `~/.claude/agents/`. A root wrapper is a thin registration shell — frontmatter (name, description, model, tools, hooks) over a one-line pointer to the child protocol at `{project}/.claude/agents/{role}.md`; a `{project}` whose child repo is not readable from the root repo inlines its protocols at root instead. Model tiers per CLAUDE.md § Model Selection
- Commands and skills: `ls .claude/commands/ .claude/skills/ ~/.claude/commands/ ~/.claude/skills/`

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

Voice lives in the fleet prompt (`{BLUEPRINT_CLONE_PATH}/templates/prompts/professor.md`), injected by `pfm` when `claude.systemPrompt` is `professor` (`pfm config show`). CLAUDE.md and every agent/skill/command carry zero voice. Cross-file dedup targets: child CLAUDE.md keeps only its delta vs root CLAUDE.md; a project agent keeps only its delta vs the project CLAUDE.md it reads at start.

### Hooks vs prompts

For things that must happen every time (formatting, validation, secret-scanning), write a hook (`.claude/settings.json` PreToolUse / PostToolUse) — deterministic, cheap. Prompts are advisory; the model can drift. Once a hook owns an invariant, delete the prompt rule that restated it — keeping both is duplication against a deterministic mechanism.

### Where harness content goes (anti-bloat routing)

- Behavioral rules → prompt files (CLAUDE.md, agents, commands, skills)
- Incident narratives ("on 2026-XX-XX...") → commit message / epic manifest (`docs/epics/{name}/`) — never prompt files
- Architectural decisions / why-this-design → epic manifest or `docs/commands/{cmd}/references/` — prompts encode the rule, not the rationale
- Voice / character flavor → the fleet prompt — zero voice in CLAUDE.md, agents, skills, commands
- Project-specific tooling → child CLAUDE.md only — never per-project agents (they inherit via parent)
- Cross-cutting templates (report format, plan shape) → one canonical reference file — never duplicated per-project

## What you own

- Root CLAUDE.md: `CLAUDE.md`; child CLAUDE.md: `{project}/CLAUDE.md`
- Agents: `.claude/agents/*.md` (root) + `{project}/.claude/agents/*.md` (child)
- Commands: `.claude/commands/*.md`; skills: `.claude/skills/*/SKILL.md`
- Scripts: `.claude/scripts/*.{sh,mjs}`; settings: `.claude/settings.json`
- Codex mirror: `.codex/` + `AGENTS.md` files + `$HOME/.codex/` — generated from the local Claude sources by `pfm codex build`; `pfm codex check` gates drift. The `codex-sync.sh` hooks run both after a Claude-source edit. Hand-written keepers: `.codex/config.toml` (except its generated `mcp_servers` fence, compiled from `.mcp.json`), `.codex/rules`
- Ledgers: `.professor/drift.md` (keep-local customization notes + update history), `.professor/retro.md` (steering inbox)

---

## Where a change lands

Classify FIRST — before any edit. The classification decides the source of truth. **Unsure? Ask the user — never guess.**

- **Framework change** (an improvement any Professor adopter could use) → edit the canonical blueprint template at `{BLUEPRINT_CLONE_PATH}` under that repo's own law and gates; its commit message names the adopter-facing change — the blueprint's release reviewers write the notes from the diff. Never put project-specific behavior into the blueprint.
- **Project customization** → edit this project's local file directly. That local file is the source of truth; it is not regenerated from the template. Add a `.professor/drift.md` line (`- {Tier/scope} — {what changed}`) only when a human-readable keep-local note is useful, never as merge machinery.
- **Engine mirror** → never edit the generated output by hand. Change its local Claude source, then run `pfm codex build` and `pfm codex check` (or the owning compiler for another engine).
- **Upstream project-template delta** → `/pfm` § Adopter update flow: `pfm update check` prints per file the exact `git diff` to read; hand-apply the parts that belong locally through this command's change flow, then `pfm update pin <local>` (accept), `pfm update ignore <template>` (never adopt), or `pfm update drop <local>` (forget). No baseline yet (the install predates `pfm init`): `pfm update adopt [--at <ref>]` once.

There is no local-stopgap-to-regeneration ceremony. A framework fix and a project customization are separate changes in their respective sources of truth.

**Standalone-skill special case:** a change to a `sources.json` skill bumps the skill's `version:` frontmatter — the framework repo's release flow ships the substance to the skill's own public repo; the Professor changelog carries only the version pointer + re-pull note.

**Retro inbox — `.professor/retro.md`:** the main-loop steering-conscience ledger (sessions append per its header; wave retros archive with their wave) — an inbox `/pcm` consumes, never a change log. The `retro` dispatch sweeps entries lacking `Resolved:`, folds each `Amend:` into the named file through the normal change flow (or rules it `judgment` — no text fix), stamps `Resolved: {date} — {where}` under the entry in place, and classifies every fold per the list above.

---

## How to process a change request

### Step 1 — Understand

Parse `$ARGUMENTS`. Dispatch first: `audit` → the **Pipeline Consistency Audit** section; `retro` → the § Where-a-change-lands retro-inbox fold pass; anything else → the change-request flow below. Common change-request categories: agent behavior, pipeline flow, conventions, new agent/command/skill, script fix, rename/restructure, settings.

### Step 2 — Audit impact

Before ANY changes, read all affected files. Grep every reference across `.claude/`, `CLAUDE.md`, child CLAUDE.md files.

**Facts verify against code, never against prior text.** Every factual claim a framework file makes about the codebase — a path, mechanism, config, protocol, count — is verified against the code at write time (scout with an Explore agent for anything non-trivial); a rewrite derives from what the code says, never from what the file used to say. Files lie; the code doesn't.

**Consistency checklist:**

- Project dir names in CLAUDE.md match actual directories
- Agent frontmatter matches actual behavior and tools needed
- worktree.sh project resolution matches directory names
- Wave-command references (`/wave:orchestrator`, `/wave:live`, `/wave:walker`) match agent names and doc paths
- Tech stack descriptions match package.json/pyproject.toml deps
- Pipeline flow in the wave commands matches agent ordering constraints

### Step 3 — Plan

Group changes: (1) **breaking** (must be atomic), (2) **non-breaking** (independent). Count the tasks per the fleet prompt § Orchestration: more than one ⇒ `speker` writes the spec directory and one agent executes each task file; edits the guard reserves for the main loop (`.claude/**`, any `CLAUDE.md`) are applied here from those task files.

### Step 4 — Execute

**Open the gate (before the edit pass).** A PreToolUse hook (`pfm-guard.sh`) denies Edit/Write to `.claude/**` and any `CLAUDE.md` (root or child) unless BOTH session-keyed markers are fresh: reading `quality/prompt.md` stamps the quality marker automatically, and the active marker is stamped with the exact command the deny message provides (it carries your session key) — run the stamp unsandboxed, a sandboxed write never lands on the filesystem the hook reads. Markers survive turn ends and slide on every allowed edit — stamp once per session; the TTL reaps only abandoned sessions. If a write is denied, follow the deny message and retry. Never route around it by disabling the hook.

**Agent edit rules:**

- Preserve YAML frontmatter format (`name`, `description`, `tools`)
- Preserve path variables — never hardcode
- Keep step numbering consistent
- Root agent descriptions must match `subagent_type` registry

**CLAUDE.md rules:**

- Keep section hierarchy — agents/commands reference sections by name
- Keep non-negotiable rules exactly as they are

**Command rules:**

- Step names stay consistent across the wave commands and the agents they cite
- Port reading instructions must match what gitter writes to ports.md

**Script rules:**

- Keep `set -euo pipefail` at the top
- Keep lock mechanism in alloc-ports.sh

### Step 5 — Verify consistency

1. Grep for stale references to old names/paths
2. Cross-reference agent tools lists
3. Pipeline completeness — every agent a wave command spawns has a definition
4. Command completeness — every command referenced in CLAUDE.md (Request Routing) has a file; every `.claude/commands/*.md` has a `description:`
5. Script references exist at stated paths
6. Directory name consistency across all files

### Step 6 — Report

Report, in order: "Infrastructure updated, N files changed" — the changes (what and why) — consistency verified (stale references none/N-fixed; pipeline flow valid; agent definitions consistent) — for a framework change, "Blueprint changed: {commit-message line}"; for a project customization, "Local source changed directly" plus the `drift.md` line when one was written — repos touched beyond this one ({BLUEPRINT_CLONE_PATH}, $HOME) with their uncommitted state, or "none" — manual verification needed (list, or "none").

---

## Pipeline Consistency Audit

Run when `$ARGUMENTS` starts with `audit`. **Read-only** — reports problems, does NOT fix them.

### Execution model — fan-out agents

Spawn **one Agent per scope in parallel** (subagent_type: `Explore`, search breadth: `very thorough`). Each agent deep-reads its entire domain — follows every reference, reads every file, verifies semantic consistency. PCM aggregates results after all agents return.

**Row tiering within a scope.** Closed-list mechanical rows — frontmatter-parses, path-exists, size limits, executable `+x`, file counts, known-name greps — MAY run as a cheap child (`Explore` or `model: haiku`) against the explicit checklist: coverage lives in the checklist, so a miss surfaces as a missing row, not a silent gap. Semantic rows — description↔behavior match, delegation sanity, route-to validity — stay on the very-thorough walker. Aggregation is unchanged.

**Scope selection:** `audit` or `audit all` → ALL scopes in parallel. `audit {scope}` → single scope.

**Agent brief template** (adapt per scope — each agent's brief carries its scope's section from § Audit scopes):

> You are auditing the Professor framework's **{SCOPE}**. Read every file listed. For each check, report one line: `PASS: {detail}` or `FAIL: {detail}` or `WARN: {detail}`. Do NOT fix anything — report only. Follow every reference, read every file, verify every claim. The project root is `{cwd}`.

### Aggregation

After all scope agents return:

1. Merge per-scope findings into a single report
2. Deduplicate findings that appear in multiple scopes
3. Assign severity: **CRITICAL** (broken reference, missing file, invariant violation — and ALWAYS any claim wrong in the REASSURING direction: promising a protection, isolation, or sanitization the code does not provide), **WARNING** (stale name, size approaching limit, weak inconsistency), **INFO** (style nit, non-blocking)
4. Count totals per severity

### Report format

Shape, in order: title "Pipeline Audit Report — {date}" — summary (scopes audited, agents fanned; total checks / passed / critical / warnings / info) — per-scope results, one PASS/FAIL/WARN-prefixed line per finding — numbered issues with severity badge + suggested fix — verdict: CLEAN, or NEEDS ATTENTION — N critical, M warnings.

Ask: "Want me to fix these issues?"

## Audit scopes

Seven scopes: `agents`, `commands`, `skills`, `pipeline`, `scripts`, `structure`, `cross-refs`. Read-only checks — report `PASS/FAIL/WARN`, never fix.

### `agents` — Walk every agent file

Files: `.claude/agents/*.md`, `{project}/.claude/agents/*.md`

- **Frontmatter validity:** every agent has `name`, `description`, `tools` — all non-empty, YAML parses cleanly
- **Path references:** extract every file path in each agent body → verify each exists on disk
- **Delegation chains:** if agent says "spawn", "Read and follow", or references another agent → verify target exists (project registry or `~/.claude/agents/`)
- **Gitter monopoly:** grep ALL agents for `git add`, `git commit`, `git push`, `git checkout`, `git merge` → ONLY `gitter.md` should contain these
- **Size limit:** no agent file >15KB
- **Inventory sync:** agent rosters match a live `ls` of every agents dir (enumerate — never recall a number)
- **Frontmatter ↔ behavior:** `tools` field lists tools the agent actually uses in its instructions

### `commands` — Walk every command file

Files: `.claude/commands/**/*.md`

- **Agent references:** every agent name/path referenced in the command → verify agent file exists
- **Doc path references:** every `$CDOCS`, `$REFS`, `docs/` path → verify target exists on disk
- **Subcommand structure:** if command defines subcommands via table/args, verify each is handled in the body
- **Route-to validity:** if this command is named in CLAUDE.md "Request Routing" (non-obvious calls + guards only), the entry → matches what the command actually handles
- **Size limit:** no command file >35KB
- **Registry coverage:** every command carries `name:` + `description:` frontmatter — the routing signal the harness injects — and the `description:` matches what the command body actually handles and names every subcommand/mode/flag the body defines (`/quality:description`); `disable-model-invocation: true` only on user-triggered-by-design commands

### `skills` — Walk every SKILL.md

Files: every SKILL.md under `.claude/` (`find .claude -name 'SKILL.md'` — includes command-embedded skills)

- **Structure:** SKILL.md exists in each skill dir, has identifiable trigger patterns
- **Skill registration:** every skill dir under `.claude/skills/` has a `description` frontmatter (auto-surfaced in the available-skills list) that names every mode/trigger the body defines; CLAUDE.md keeps only the one-line Skills pointer, not a per-skill table
- **Registry, not roster:** the skill's `description:` carries its triggers (CLAUDE.md lists no skills — the harness indexes them); a CLAUDE.md mention exists only for a guard or a mandatory-load obligation

### `pipeline` — Walk the wave chain end-to-end

Files: `.claude/commands/wave/*.md` (project: refine, orchestrator, live, walker), `~/.claude/commands/wave/*.md` (machine-global: builder, ccc), all agents they reference

- **Reference resolution:** every "Read and follow" path → target file exists
- **Agent spawn validity:** every `subagent_type` referenced → matches a registered agent name/description in `.claude/agents/`, child agents, or `~/.claude/agents/`
- **Path variables:** `$DOCS`, `$WORKTREE` used — no hardcoded `docs/dev/` or `.worktrees/` paths
- **Step ↔ prose match:** step labels the commands cite (`O0`–`O3`, `W1`–`W8`) exist in the file that owns them
- **Script references:** worktree.sh, alloc-ports.sh paths → files exist and are executable
- **Flow integrity:** refine → orchestrator → builder → walker across commands; developer → QA → reviewer → gitter within a wave — no step references an agent from a later phase

### `scripts` — Walk each script

Files: `.claude/scripts/*.{sh,mjs}`

- **Existence & permissions:** each script exists and is executable (`+x`)
- **Referential integrity:** grep agents/commands/settings.json for each script name → paths used to call it are correct
- **Safety headers:** `set -euo pipefail` present at top of every `.sh`
- **No hardcoded paths:** no absolute paths or project-specific paths that should be variables

### `structure` — Walk repo skeleton

Files: project dirs, CLAUDE.md files, permanent docs, lock files

- **Project dirs:** every roster project exists (`{project}/`, repeated per entry)
- **Child CLAUDE.md:** each project dir has a `CLAUDE.md`
- **Child agents:** each project dir has `.claude/agents/`; rosters match a live `ls`
- **Permanent docs:** `docs/agents/`, `docs/commands/` dirs exist with expected subdirs
- **Stale names:** grep all CLAUDE.md files and agents for old/renamed project names or typos
- **Package managers:** each roster project's lock file (`{PROJECT_PKG_MGR}` per project) present
- **Codex mirror:** `pfm codex check` exits 0 — report its output verbatim; a non-zero exit names each generated Codex artifact (AGENTS.md, `.codex/`, `$HOME/.codex/`) that is MISSING, STALE, ORPHANed, or CONFLICTing with an unmarked file
- **Baseline:** `pfm update check` runs (a `.professor/baseline.json` exists) — report its counts verbatim; FAILED output is a finding, never "current"

### `cross-refs` — The glue between domains

Catches what no single-domain audit can see. Reads across ALL domains simultaneously.

- **Routing ↔ commands:** every command/skill named in CLAUDE.md "Request Routing" (the non-obvious calls + guards only — most route by self-indexing) → file exists and handles claimed scope
- **Agent counts ↔ reality:** a live `ls` of every agents dir → matches § Inventory's derivation rules (rosters: § Authoring conventions, no-rosters law)
- **Command count ↔ reality:** every `.claude/commands/*.md` carries `name:` + `description:` frontmatter (the harness registry) — CLAUDE.md carries no command roster
- **Skill count ↔ reality:** every dir in `ls .claude/skills/` has valid SKILL.md frontmatter; CLAUDE.md Skills section is a pointer, not a list (nothing to drift)
- **Frontmatter validity:** every agent has non-empty `name`/`description`/`tools`; root agent `name` matches its `subagent_type` registry entry
- **Doc ownership:** CLAUDE.md doc-ownership claims → claimed paths exist
- **Invariant spot-check:** sample 3 critical invariants from § Critical invariants → verify they hold in the actual files
- **Co-loaded duplication sweep:** the same rule stated in two co-loaded files (pcm.md ↔ root CLAUDE.md ↔ quality/prompt.md; child CLAUDE.md ↔ root; a command ↔ its reference cards) — each rule lives in exactly ONE canonical home, others carry at most a pointer (quality/prompt anti-patterns 3 & 11)
- **Claims ↔ code:** spot-check factual claims (paths, mechanisms, configs, counts) in root + child CLAUDE.md against the code — a claim wrong in the reassuring direction is CRITICAL, never INFO

---

## Special Operations

**Full rename:** Grep ALL occurrences → update agents → update CLAUDE.md → update the wave commands → final grep for zero stale refs.

**New agent:** Create `.claude/agents/{name}.md` → update pipeline if needed (the Inventory derives; nothing to count).

**New skill:** Create `.claude/skills/{name}/SKILL.md` → no CLAUDE.md edit needed (skills self-index from `description:` frontmatter).

**New command:** Create `.claude/commands/{name}.md` with a `description:` → it self-indexes; add to CLAUDE.md "Request Routing" ONLY if it's a non-obvious call or a guard.

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

1. Do the Inventory derivations still enumerate correctly? (`ls .claude/agents/`, `ls .claude/commands/`, `find .claude -name 'SKILL.md'` — skills live under `.claude/skills/` AND embedded in command dirs)
2. Are the critical invariants still true?
3. Did any project directories or table structures change?
4. Is the system wiring map still accurate?

If anything is stale, update this file before completing the report. This command must never give outdated advice about its own pipeline.

---

## Rules

- **User-ordered only** — framework files change ONLY on the user's explicit in-session command, never autonomously, never as automation, never as a side effect of other work; an improvement spotted mid-task is proposed, not applied
- **Never break the pipeline** — atomic changes for breaking modifications
- **Never weaken non-negotiable rules** — ethics, privacy, code quality are sacred
- **Never remove safety checks** — QA gates, merge guards, worktree isolation
- **Preserve agent autonomy** — self-contained, no circular dependencies
- **Keep it DRY** — reference CLAUDE.md from agents, don't duplicate
- **Sync across projects** — change in one place = reflect everywhere
- **Prefer deletion over addition** — root's surgical-changes law governs the rest
- **Research before writing** — verify domain content before adding. Structural changes don't need research
- **Always consider token budget** — define once, reference everywhere
- **Routing-gate every fan-out** — spawn agents only for declared scope; the consolidator may demand additions; fall back to full fan-out only when scope is undeclared
- **Every pipeline artifact names its consumer** — before adding a report/file an agent writes, name who reads it downstream; write-only artifacts are banned
- **Delta-structure repeatedly-rewritten state files** — rewritten resume brief on top, append-only archive below a marker; never full-file rewrites
- **Exact-slice agent inputs** — when carving a manifest for parallel agents, each gets its exact slice + a thin shared header; a shared contract lives once in a shared file every brief that needs it names (a `0-` file in a `speker` directory), never copied per agent
- **Exact per-role read lists in spawn briefs** — "read ALL docs in {dir}/" licenses every agent to read everything; name each role's exact read list
- **One common spawn contract per orchestrator** — hoist rules shared across spawn blocks into a single contract each block references, never restated per block
- **Every check names what its OWN broken state reports** — authoring or editing any instrument that returns a verdict (probe, health check, gate, audit, walker, lint), ask what it reports when IT is broken rather than when the world is clean. Same answer both ways = not a check but a coincidence detector, and it will bless the failure it exists to catch (`kill -0` cannot distinguish a healthy waiter from a reparented deaf one; `PPID ≠ 1` can — a pane capture on the wrong socket returns silence identical to a quiet chat; a capture that cannot reach its target exits non-zero). Build the distinguishing signal INTO the instrument: a law forbidding the mistake is strictly weaker than a check detecting it
