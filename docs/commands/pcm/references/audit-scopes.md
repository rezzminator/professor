# PTM audit — per-scope deep checks

Loaded by `/pfm audit`: PTM reads this file when composing fan-out briefs; each agent's brief carries its scope's section below (adapted per the brief template in `pcm.md § Execution model`). Read-only checks — report `PASS/FAIL/WARN`, never fix.

## `agents` — Walk every agent file

Files: `.claude/agents/*.md`

- **Frontmatter validity:** every agent has `name`, `description`, `tools` — all non-empty, YAML parses cleanly
- **Path references:** extract every file path in each agent body → verify each exists on disk
- **Delegation chains:** if agent says "spawn", "Read and follow", or references another agent → verify target exists
- **Gitter monopoly:** grep ALL agents for `git add`, `git commit`, `git push`, `git checkout`, `git merge` → ONLY `gitter.md` should contain these
- **Size limit:** no agent file >15KB
- **Inventory sync:** agent rosters match a live `ls` of every agents dir (enumerate — never recall a number)
- **Frontmatter ↔ behavior:** `tools` field lists tools the agent actually uses in its instructions

## `commands` — Walk every command file

Files: `.claude/commands/**/*.md`

- **Agent references:** every agent name/path referenced in the command → verify agent file exists
- **Doc path references:** every `$CDOCS`, `$REFS`, `docs/` path → verify target exists on disk
- **Subcommand structure:** if command defines subcommands via table/args, verify each is handled in the body
- **Route-to validity:** if this command is named in CLAUDE.md "Request Routing" (non-obvious calls + guards only), the entry → matches what the command actually handles
- **Size limit:** no command file >35KB
- **Registry coverage:** every command carries `name:` + `description:` frontmatter — the routing signal the harness injects — and the `description:` matches what the command body actually handles and names every subcommand/mode/flag the body defines (`pcm.md § Authoring conventions — Descriptions`); `disable-model-invocation: true` only on user-triggered-by-design commands

## `skills` — Walk every SKILL.md

Files: every SKILL.md under `.claude/` (`find .claude -name 'SKILL.md'` — includes command-embedded skills)

- **Structure:** SKILL.md exists in each skill dir, has identifiable trigger patterns
- **Skill registration:** every skill dir under `.claude/skills/` has a `description` frontmatter (auto-surfaced in the available-skills list) that names every mode/trigger the body defines; CLAUDE.md keeps only the one-line Skills pointer, not a per-skill table
- **Registry, not roster:** the skill's `description:` carries its triggers (CLAUDE.md lists no skills — the harness indexes them); a CLAUDE.md mention exists only for a guard or a mandatory-load obligation

## `templates` — Walk the shipped product

Files: `templates/**`, `docs/{BLUEPRINT,SETUP,PLACEHOLDERS,RELEASE}.md`, `templates/refresh-map.json`, root `README.md`

This is the scope with adopters downstream of it — a FAIL here ships.

- **Internal reference resolution:** every template referencing another template, agent, command, script, or reference card → the target exists under `templates/` (a pointer that only resolves in the source project is a broken install)
- **Placeholder integrity:** every `{TOKEN}` in a template is registered in `PLACEHOLDERS.md`; every registered token is used or knowingly retired; no template carries a source-project value where a token belongs
- **SETUP coverage:** every file under `templates/` has a write step in `SETUP.md`; every path `SETUP.md` writes has a template
- **Cast consistency:** `README.md` and `BLUEPRINT.md` agent/command/skill lists match a live `ls` of `templates/project/agents/` and `templates/project/commands/`
- **Refresh map:** every mapped template's live source path is stated; UNMAPPED templates are named (`refresh-scope.sh scan` is the mechanical check — run it and report its output verbatim, including whether it ran at all)
- **Leak sweep:** `scripts/leak-check.sh --files <changed templates>` — brand, PII, machine-absolute paths. Report the command's own exit status; a check that did not run is never a PASS
- **Size limits:** no command template >35KB, no agent template >15KB

## `scripts` — Walk each script

Files: `.claude/scripts/*.{sh,mjs}`

- **Existence & permissions:** each script exists and is executable (`+x`)
- **Referential integrity:** grep agents/commands for each script name → paths used to call it are correct
- **Safety headers:** `set -euo pipefail` present at top of every bash `.sh` — named exceptions: fail-open hooks (`explore-deny.sh`, `format-md.sh`) run `set -uo pipefail` by design, and POSIX `#!/bin/sh` scripts (`memory-*.sh`) carry no `pipefail`
- **No hardcoded paths:** no absolute paths or project-specific paths that should be variables

## `structure` — Walk repo skeleton

Files: project dirs, CLAUDE.md files, permanent docs, lock files

- **Project dirs:** every roster project named in root `CLAUDE.md` and `.claude/scripts/dev.sh` exists on disk, and every project dir on disk is named in both
- **Permanent docs:** `docs/commands/` exists with the reference dirs its commands name
- **Stale names:** grep `CLAUDE.md`, agents, and commands for old/renamed project names or typos
- **Manifests:** each roster project's manifest is present (`pfm/go.mod`) and its stack description in `CLAUDE.md` matches it
- **Install state:** `.professor/{VERSION,manifest.json,drift.md}` exist and `VERSION` agrees with the root `VERSION` file
- **Codex mirror:** `pfm codex check .` exits 0 — report its output verbatim; a non-zero exit names each generated Codex artifact (AGENTS.md, `.codex/`, `$HOME/.codex/`) that is MISSING, STALE, ORPHANed, or CONFLICTing with an unmarked file

## `cross-refs` — The glue between domains

Catches what no single-domain audit can see. Reads across ALL domains simultaneously.

- **Routing ↔ commands:** every command/skill named in CLAUDE.md "Request Routing" (the non-obvious calls + guards only — most route by self-indexing) → file exists and handles claimed scope
- **Agent counts ↔ reality:** a live `ls` of every agents dir → matches `pcm.md § Inventory`'s derivation rules (rosters: `pcm.md § Authoring conventions`, no-rosters law)
- **Command count ↔ reality:** every `.claude/commands/*.md` carries `name:` + `description:` frontmatter (the harness registry) — CLAUDE.md carries no command roster
- **Skill count ↔ reality:** every dir in `ls .claude/skills/` has valid SKILL.md frontmatter; CLAUDE.md Skills section is a pointer, not a list (nothing to drift)
- **Frontmatter validity:** every agent has non-empty `name`/`description`/`tools`; root agent `name` matches its `subagent_type` registry entry
- **Doc ownership:** CLAUDE.md doc-ownership claims → claimed paths exist
- **Invariant spot-check:** sample 3 critical invariants from `pcm.md § Critical invariants` → verify they hold in the actual files
- **Co-loaded duplication sweep:** the same rule stated in two co-loaded files (pcm.md ↔ root CLAUDE.md ↔ quality/prompt.md; child CLAUDE.md ↔ root; a command ↔ its reference cards) — each rule lives in exactly ONE canonical home, others carry at most a pointer (quality/prompt anti-patterns 3 & 11)
- **Claims ↔ code:** spot-check factual claims (paths, mechanisms, configs, counts) in root + child CLAUDE.md against the code — a claim wrong in the reassuring direction is CRITICAL, never INFO
