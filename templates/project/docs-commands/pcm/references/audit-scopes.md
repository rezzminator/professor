# PCM audit — per-scope deep checks

Loaded by `/pcm audit`: the `pcm` agent reads this file when composing fan-out briefs; each agent's brief carries its scope's section below. Read-only checks — report `PASS/FAIL/WARN`, never fix.

## `agents` — Walk every agent file

Files: `.claude/agents/*.md`, `{project}/.claude/agents/*.md`

- **Frontmatter validity:** every agent has `name`, `description`, `tools` — all non-empty, YAML parses cleanly
- **Path references:** extract every file path in each agent body → verify each exists on disk
- **Delegation chains:** if agent says "spawn", "Read and follow", or references another agent → verify target exists (project registry or `~/.claude/agents/`)
- **Gitter monopoly:** grep ALL agents for `git add`, `git commit`, `git push`, `git checkout`, `git merge` → ONLY `gitter.md` should contain these
- **Size limit:** no agent file >15KB
- **Inventory sync:** agent rosters match a live `ls` of every agents dir (enumerate — never recall a number)
- **Frontmatter ↔ behavior:** `tools` field lists tools the agent actually uses in its instructions

## `commands` — Walk every command file

Files: `.claude/commands/**/*.md`

- **Agent references:** every agent name/path referenced in the command → verify agent file exists
- **Doc path references:** every `$CDOCS`, `$REFS`, `docs/` path → verify target exists on disk
- **Subcommand structure:** if command defines subcommands via table/args, verify each is handled in the body
- **Route-to validity:** if this command is named in CLAUDE.md § Rules (non-obvious calls + guards only), the entry → matches what the command actually handles
- **Size limit:** no command file >35KB
- **Registry coverage:** every command carries `name:` + `description:` frontmatter — the routing signal the harness injects — and the `description:` matches what the command body actually handles and names every subcommand/mode/flag the body defines (`/quality:description`); `disable-model-invocation: true` only on user-triggered-by-design commands

## `skills` — Walk every SKILL.md

Files: every SKILL.md under `.claude/` (`find .claude -name 'SKILL.md'` — includes command-embedded skills)

- **Structure:** SKILL.md exists in each skill dir, has identifiable trigger patterns
- **Skill registration:** every skill dir under `.claude/skills/` has a `description` frontmatter (auto-surfaced in the available-skills list) that names every mode/trigger the body defines; CLAUDE.md carries no per-skill table
- **Registry, not roster:** the skill's `description:` carries its triggers (CLAUDE.md lists no skills — the harness indexes them); a CLAUDE.md mention exists only for a guard or a mandatory-load obligation

## `pipeline` — Walk the flight chain end-to-end

Files: `~/.claude/agents/flights-*.md` and `~/.claude/commands/flights/doctor.md` (machine-global), all agents they reference

- **Reference resolution:** every "Read and follow" path → target file exists
- **Agent spawn validity:** every `subagent_type` referenced → matches a registered agent name/description in `.claude/agents/`, child agents, or `~/.claude/agents/`
- **Path variables:** `$DOCS`, `$WORKTREE` used — no hardcoded `docs/dev/` or `.worktrees/` paths
- **Return tokens ↔ their writers:** every token a `flights-*` agent's `description:` returns (`DONE`, `PARTIAL`, `PASS`, `FIXED`, `FAIL`) is one its body's § Return writes
- **Script references:** worktree.sh, alloc-ports.sh paths → files exist and are executable
- **Flow integrity:** main chat → a `flights-foreman` tier → main chat → `flights-lander` → gitter; no step references an agent from a later phase

## `scripts` — Walk each script

Files: `.claude/scripts/*.{sh,mjs}`

- **Existence & permissions:** each script exists and is executable (`+x`)
- **Referential integrity:** grep agents/commands/settings.json for each script name → paths used to call it are correct
- **Safety headers:** `set -euo pipefail` present at top of every `.sh`
- **No hardcoded paths:** no absolute paths or project-specific paths that should be variables

## `structure` — Walk repo skeleton

Files: project dirs, CLAUDE.md files, permanent docs, lock files

- **Project dirs:** every roster project exists (`{project}/`, repeated per entry)
- **Child CLAUDE.md:** each project dir has a `CLAUDE.md`
- **Child agents:** each project dir has `.claude/agents/`; rosters match a live `ls`
- **Permanent docs:** the hub `docs/_index.md` and `docs/commands/` exist, with expected subdirs
- **Stale names:** grep all CLAUDE.md files and agents for old/renamed project names or typos
- **Package managers:** each roster project's lock file (`{PROJECT_PKG_MGR}` per project) present
- **Codex mirror:** `engine-sync codex check` exits 0 — report its output verbatim; a non-zero exit names each generated Codex artifact (AGENTS.md, `.codex/`, `$HOME/.codex/`) that is MISSING, STALE, ORPHANed, or CONFLICTing with an unmarked file
- **Baseline:** `pfm doctor --project-updates` runs (a `.professor/baseline.json` exists) — report its counts verbatim; FAILED output (exit 3) is a finding, never "current"

## `cross-refs` — The glue between domains

Catches what no single-domain audit can see. Reads across ALL domains simultaneously.

- **Routing ↔ commands:** every command/skill named in CLAUDE.md § Rules (the non-obvious calls + guards only — most route by self-indexing) → file exists and handles claimed scope
- **Agent counts ↔ reality:** a live `ls` of every agents dir → matches the `pcm` agent's rule that rosters are discovered, never a recalled count (rosters: `/quality:claude-md`)
- **Command count ↔ reality:** every `.claude/commands/*.md` carries `name:` + `description:` frontmatter (the harness registry) — CLAUDE.md carries no command roster
- **Skill count ↔ reality:** every dir in `ls .claude/skills/` has valid SKILL.md frontmatter; CLAUDE.md lists no skills (nothing to drift)
- **Frontmatter validity:** every agent has non-empty `name`/`description`/`tools`; root agent `name` matches its `subagent_type` registry entry
- **Doc ownership:** CLAUDE.md doc-ownership claims → claimed paths exist
- **Invariant spot-check:** sample 3 critical invariants from the `pcm` agent's § Critical invariants → verify they hold in the actual files
- **Co-loaded duplication sweep:** the same rule stated in two co-loaded files (pcm.md ↔ root CLAUDE.md ↔ quality/prompt.md; child CLAUDE.md ↔ root; a command ↔ its reference cards) — each rule lives in exactly ONE canonical home, others carry at most a pointer (quality/prompt anti-patterns 3 & 11)
- **Claims ↔ code:** spot-check factual claims (paths, mechanisms, configs, counts) in root + child CLAUDE.md against the code — a claim wrong in the reassuring direction is CRITICAL, never INFO
