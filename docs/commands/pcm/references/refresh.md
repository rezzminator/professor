# The Refresh Pass — Re-derive the Blueprint from Live Source

Executed inside `/pfm:release` (step 3). Re-derives the blueprint from the CURRENT `.claude/` and `CLAUDE.md` state. Edit files directly inside this repo's `templates/project/` tree — this repo IS the upstream clone.

**Scope (incremental):** `templates/refresh-map.json` maps every template to its live source(s) + the SHA-256 of each as of the last sync. `scripts/refresh-scope.sh scan` proves unchanged sources untouched — their templates are skipped; re-derive only CHANGED templates; UNMAPPED-LIVE files get a mapping ruling. `curated` templates have no live source and are never auto-derived. `refresh-scope.sh regen` re-baselines the hashes at release end.

**Update mechanism context:** Adopters install from a tagged blueprint. `pfm init` scaffolds project templates once and records per-file template pins in `.professor/baseline.json`; the local project files then own truth. `pfm update check` reports `UPDATED`, `NEW`, `GONE-UPSTREAM`, and `LOCAL-DELETED` mappings without writing. The session reviews each printed template diff, hand-applies wanted changes, and advances accepted pins. Machine-global symlinks update through the blueprint clone, and engine mirrors rebuild from local sources.

Cross-conversation context persists via **Epics** — initiative-level manifest files (`docs/epics/{name}/manifest.md`) with lifecycle tracking (PLANNING → IN_PROGRESS → SHIPPED).

---

## Contents

- [Three tiers](#three-tiers)
- [Source files to mine](#1-source-files-to-mine)
- [Tier-aware transformations](#2-tier-aware-transformations)
- [Output structure](#3-output-structure)
- [SETUP.md install interview](#4-setupmd--interactive-install-interview)
- [Public README](#5-public-readme)
- [Process rules](#6-process-rules)
- [Report](#7-report-after-the-refresh-pass)

---

## Three Tiers

| Tier | Description | Ships | Gets parameterized |
| ---------------------------- | ------------------------------------------------------ | ------------------------------------------------------------------ | --------------------------------------------------------------------------------- |
| **A — Universal archetypes** | Personalities that work in any domain. Voice IS value. | Full character, structure, identity | Domain REFERENCES inside the character (the Professor's PhDs, a persona's stack traces) |
| **B — Domain archetypes** | Roles every project needs, content domain-shaped | Archetype skeleton: identity, voice, charter, modes, doc structure | Regulation, knowledge domain, user persona, market segment — filled via interview |
| **C — Pure mechanics** | Infrastructure agents and plumbing | Mechanics only — no character | Tech-specific commands (test runner, package manager, build tool) |

### Tier assignments

**Tier A** — `Professor` (persona), `/pfm` (with its `update` and `release` subcommands), `/flights:{spec,orchestrate-nested,orchestrate-live,orchestrate-cross-harness,audit}`, `/dev`, `/save` **Tier B** — `/officer` `{REGULATION}`, `/mentor` `{MARKET_SEGMENT}`, `/marketer` `{CHANNEL_LANDSCAPE}` **Tier C** — root agents (gitter), scripts (worktree.sh, alloc-ports.sh, dev.sh), per-project testing manuals (`/{project}-testing-manual`) and the optional per-project specialists (ui-ux, db-admin, devops, ai-engineer)

### Preservation (untouchable across tiers)

- Voice/tone of every Tier A character
- Archetype identity of Tier B commands
- Pipeline mechanics (planner → architect → developer → QA → gitter; worktree isolation; only-gitter-touches-git; QA gates pre+post merge; path variables)
- Discipline frame (zero-tolerance tests, mock policy, never-destructive-git, never-edit-main)

### Placeholders (project-specific → generic at refresh)

> Human copy — the executable copy is `scripts/placeholder-map.tsv`, applied by `scripts/genericize.sh` as the deterministic first pass on every re-derived template; edit both together. The LLM hand-judges structure only (roster collapsing, domain nouns, persona metaphors).

- `{PROJECT_NAME}` (and any former brand the repo was renamed from — a rename orphans the old name in the blueprint source, so scrub both), per-project directories → `{PROJECT_NAME}`, `{project-a}` etc.
- `Professor` → keep with "rename if you want" comment
- domain/user nouns (the project's subject matter, its users, its work units) → `{DOMAIN_NOUN}`, `{USER_NOUN}`
- the project's regulatory frame, jurisdiction, and legal-entity type → `{REGULATION}`, `{JURISDICTION}`, `{LEGAL_ENTITY_TYPE}`
- All tech specifics (transcription/AI providers, frameworks, ORMs, mobile/web stacks, API layers, databases, infra/cloud/hosting) → `{TECH_STACK_PLACEHOLDER}` per role
- Ports → `{PROJECT_PORT}` / `{PORT_DEFAULTS}`; package managers/test runners → `{PROJECT_PKG_MGR}`, `{PROJECT_TEST_RUNNER}`
- Blueprint self-references (`{BLUEPRINT_REPO}`, `{GH_USER}`, `{BLUEPRINT_CLONE_PATH}`) → resolved at install: a user with push access to the canonical repo targets it directly; everyone else targets their own fork

Character names (Professor, and any persona the install adds) ship as **default names with "rename if you want" instruction**. Concrete beats abstract.

---

## 1. Source files to mine

From the project repo:

- `CLAUDE.md` (root), `.claude/agents/*.md`, `.claude/commands/*.md` (Tier A+B, including command directories like `.claude/commands/pfm/`, `.claude/commands/audit/`, `.claude/commands/quality/`), `.claude/skills/*/SKILL.md` (bundled + domain-hydrated only — see next bullet), `.claude/scripts/*.sh`
- **Source-fetched skills** (`360`, `ghostwriter`, `vision-factory`) — never vendor a `SKILL.md` copy for these; they live in their own canonical repos and a stale copy is the exact drift this avoids. Refresh maintains only `templates/project/skills/sources.json` (name → repo); SETUP clones each at install. `deep-rr` lives in-tree at `workflows/deep-rr/` (ships with the blueprint clone) — not source-fetched; it updates when the blueprint clone updates, not independently.
- `docs/epics/` structure — Epics section of CLAUDE.md, manifest format, lifecycle, ownership rules
- `docs/agents/` scaffold — the hub `_index.md` format, the `standards.md` skeleton, and the cluster convention (structure only, NEVER doc content — every adopter's documentation body is their own)
- The source's per-project structure → mine it INTO the generic **roster PATTERN**: express each per-project file/section ONCE with `{project}` tokens (one representative project as the shape). NEVER bake the source's project count or role names into a template — the source's concrete roster (its N projects, those roles) is an install instance SETUP expands per entry, not template structure. A template must read correctly at roster size 1. See `PLACEHOLDERS.md` § "Project roster".

## 2. Tier-aware transformations

**Tier A:** KEEP voice/tone/structure/character/pipeline mechanics. REPLACE project identifiers + tech specifics with placeholders (the Professor's qualification is fixed prose — "15+ PhDs, one in whatever area the work touches" — a discipline roster found live genericizes to that line, never to slots). KEEP Epics section structure (manifest format, lifecycle, ownership rules) — it is domain-agnostic. **Persona voice is load-bearing — PARAMETERIZE a persona's generic sections (swap domain refs for placeholders); NEVER delete or trim them.** Worked voice examples and the Verdict examples ARE the value; mirror the live persona section-for-section, genericized — a thinned persona ships a weaker character. When a live persona carries a section the blueprint lacks, add it (genericized), never drop it.

**Tier B:** KEEP archetype skeleton. REPLACE domain content with named placeholders:

- **Officer:** `{REGULATION}`, `{REGULATION_FRAMEWORK_DOCS}`, `{ENFORCEMENT_AUTHORITY}`, `{DATA_SUBJECT_RIGHTS}`, `{INCIDENT_NOTIFICATION_TIMELINE}`
- **PM:** `{USER_PERSONA}`, `{PRODUCT_DOMAIN}`, `{USER_DAILY_WORKFLOW}`, `{USER_PAIN_POINTS}`
- **Mentor:** `{MARKET_SEGMENT}`, `{JURISDICTION}`, `{LEGAL_ENTITY_TYPE}`, `{FUNDING_LANDSCAPE}`, `{REGULATORY_BODIES}`
- **Marketer:** `{CHANNEL_LANDSCAPE}`, `{TARGET_LANGUAGE}`, `{COMPETITIVE_LANDSCAPE}`, `{INDUSTRY_CONFERENCES}`
- **KM:** `{KNOWLEDGE_DOMAIN}`, `{KNOWLEDGE_TAXONOMY}`, `{KNOWLEDGE_CONSUMERS}`, `{SOURCE_AUTHORITIES}`

**Tier C:** Strip tech specifics, keep structure, no character.

## 3. Output structure

```
professor/            ← this repo
├── README.md, INSTALL.md, CHANGELOG.md, VERSION, LICENSE
├── docs/
│   └── README.md, BLUEPRINT.md, SETUP.md, RELEASE.md, ARCHITECTURE.md, PLACEHOLDERS.md, references/
└── templates/
    ├── refresh-map.json
    ├── themes/       (curated statusline themes — no live source)
    ├── global/       (machine-global originals — agents/, commands/, skills/; `pfm install` symlinks them into engine registries; agent `.toml` twins are release-generated beside their originals and out of this map's scope)
    └── project/      (per-install templates — CLAUDE.md, agents/, commands/, skills/, scripts/, docs-agents/, docs-commands/, workflows/, epics/, codex/)
```

Rosters live in the tree, not here — `ls` the scope dir and read `refresh-map.json` for each file's live source or `curated` ruling. Two annotations that govern the refresh pass: source-fetched skills (each scope's `skills/sources.json`) are cloned from their canonical repos at install and never vendored; deep-rr ships in-tree at `workflows/deep-rr/` (updates with the blueprint clone, not independently).

## 4. SETUP.md — interactive install interview

Exports an interview Claude conducts before touching files. Structure:

**Phase 1 — Interview** (8 questions in order):

1. Project identity (one sentence)
2. Character name & voice (keep Professor or rename?)
3. Project structure (single/monorepo, subproject count + purpose)
4. Tech stack per (sub)project (lang, framework, pkg mgr, test runner, build tool, DB, infra)
5. Professor's disciplines (10+ PhDs — what fits your domain?)
6. Tier B opt-ins: Officer (regulations?), KM (domain?), PM (persona?), Mentor (market+jurisdiction?), Marketer (channels+language?)
7. Sacred ground ("do no harm" in your domain)

**Phase 2 — Customization:** Rewrite every template replacing placeholders with interview answers. The per-project blocks MUST be materialized from the actual project roster: one testing manual per entry, a specialist block only where that project wants one; delete every block for a project the roster does not list, and fail if any referenced agent or manual path does not exist.

**Phase 2.5 — Skill Knowledge Hydration (domain-hydrated skills):**

Skills ship as **empty shells** when their content is project-specific — the structure (frontmatter, headings, report format) is universal, but the audit categories, detection patterns, file paths, and domain concerns must be researched per project.

| Skill | What's universal (ships) | What's project-specific (hydrated by RR) |
| --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Analysis Protocol (in the fleet prompt, `pfm/harness-prompts/share/head.md`) | Three-lens protocol (CS + domain + compliance), step sequence, report format, AI/ML audit mode structure | Domain lens content (replaces Psychology lens), compliance framework, cross-disciplinary intersections, AI/ML audit categories + anti-patterns (if project has an AI pipeline subproject) — the refresh interview hydrates this lens directly in the fleet prompt |
| `audit:code-hygiene` | Category structure (ghost fields, dead code, stale deps, arch smells, type safety, naming, quality) | Per-category detection patterns, file paths, known hotspots, linter coverage gaps, project-specific report examples |
| `audit:security` | OWASP category structure (8A-8I), severity guide, report format | Domain-specific PHI/data sensitivity rules, external API checks, framework-specific vulnerabilities, compliance-driven sub-categories |

**Hydration process:**

1. For each domain-hydrated skill, check if the project has enough context from the interview (Phase 1) to run RR.
2. **If project is defined enough** → run `RR codebase` targeting the project's source to fill each command's knowledge base. The RR agent reads the actual code, identifies patterns, file paths, anti-patterns, and writes the domain-specific sections into the command's body (`.claude/commands/audit/{code-hygiene,security}.md`).
3. **If project is NOT defined enough** (new project, no code yet, or user skipped stack details) → write the skill with the universal structure but mark domain sections as empty:

```markdown
## Category N — {category name}

> **KNOWLEDGE BASE EMPTY** — This section needs project-specific detection patterns.
> Run the Professor's Analysis Protocol or `/audit:code-hygiene` after the codebase has enough code to analyze.
> The Professor will surface this gap: "Knowledge base is empty, waiting for user specification to fill it in."
```

4. **Professor behavior with empty commands:** When a domain-hydrated audit command is invoked and its knowledge base sections are empty, the Professor MUST NOT improvise. Instead: state which sections are empty, ask the user to either (a) provide the specification now, (b) point to code/docs to RR against, or (c) defer. The Professor stays in this loop until the command is filled — never proceeds with best-effort guessing on an empty knowledge base.

5. **Re-hydration:** User can re-run hydration at any time: "fill `/audit:security`" or "hydrate the audit commands" → triggers RR against current codebase to fill/update empty sections.

**Phase 2.6 — Host tooling probe (git-host bridge):** Check the install machine for `gh` and `glab` (`command -v`). For each present, write a one-file host command at `.claude/commands/h/{gh|glab}.md` (the `h:` host namespace) whose `description` records that the CLI is available on this host for {GitHub|GitLab} operations. It carries no procedure — it is the bridge that tells the Professor which CLI to drive: an adopter on GitLab forks + releases professor through `/h:glab`, a GitHub adopter through `/h:gh`, and `/pfm:release` and gitter read this marker to target the right host. These host-local bridges are KEEP-LOCAL — excluded from the portable blueprint. Absent tools get no command. Then resolve the blueprint repo target: if the user has push access to the canonical repo, set `{BLUEPRINT_REPO}`/`{GH_USER}`/`{BLUEPRINT_CLONE_PATH}` to it; otherwise have them fork it and use the fork.

**Phase 3 — Smoke test:** Run `/dev status`, then one tiny `/flights:orchestrate-live` task and watch its project checks.

## 5. Public README

If the repo-root `README.md` is missing → write it from the template below. If it exists → diff against the template; overwrite only if the template changed.

> Expand this structural outline into the full README. Keep it terse, opinionated, pitch-forward.

```
# Professor — Multi-Agent Claude Code Pipeline

One-paragraph pitch: portable .claude/ that turns Claude Code into a self-disciplined engineering team with character. Personality is load-bearing.

## What you get
- Full cast (Professor, Audit + Tier B opt-ins)
- Pipeline (planner→architect→developer→QA→gitter)
- Worktree isolation + port allocation
- Single git owner (gitter)
- Self-improvement at source (/pfm)
- Scaffold-and-own updates (`pfm update check` — reported template diffs, reviewed hand application, per-file pins)
- Epics — cross-conversation context persistence via manifest files (PLANNING → IN_PROGRESS → SHIPPED)
- Path conventions ($DOCS, $WORKTREE, $CDOCS)
- Documentation discipline (one agent writes permanent docs)

## Quick start
install pfm, cd your-project, `pfm init .`, claude → follow the printed SETUP.md install interview → customize → smoke test

## The cast — Tier A
Professor, /pfm, /flights:{spec,orchestrate-nested,orchestrate-live,orchestrate-cross-harness,audit}, /dev

## Tier B (opt-in)
/officer, /mentor, /marketer

## The five load-bearing walls
1. Only gitter touches git
2. QA gates the merge (pre+post)
3. Path variables, not hardcoded
4. Worktree isolation per pipeline
5. Self-improvement at the source

## When to use it
✅ Multi-project monorepos, complex pipelines, teams losing work to half-finished branches, decision-audit matters, agents with voice
⚠️ Overkill for: 200-line scripts, throwaway prototypes, projects where main can break

## Origin & maintenance
Auto-regenerated from the live upstream repo. Maintained by @{GH_USER}. Issues/PRs welcome (open issue first for large changes).

## License
MIT
```

## 6. Process rules

- This repo is the ONE source of truth. Before editing: `git fetch origin && git pull --ff-only origin main`.
- Use `Edit` for surgical updates, `Write` for new files/full rewrites.
- Preserve manually-curated commentary unless it contradicts current state.
- Do NOT delete `INSTALL.md`, `LICENSE`, or hand-curated root files.

## 7. Report after the refresh pass

```
Refresh pass complete in templates/project/. {N} files updated, {M} unchanged.
Tier A: {count} | Tier B: {count} | Tier C: {count}
Sources mined: {list}
Generalizations: identifiers→placeholders {count}, tech→placeholders {count}, domain→slots {count}
Character preservation: Professor ✓
Continuing release.
```

## The pass — how a refresh runs

Driven by `/pfm:release --from {live-project-root}` (step 3); there is no standalone command. The tier table, preservation list and placeholder law above are the rules every worker applies — this section owns HOW the pass runs: cheaply, in reviewed batches.

### The cost law

The pass is diff-driven, never file-driven. Whole-file reads are what make a refresh unaffordable, and a 5,000-line sweep dies of context long before it dies of difficulty.

- The orchestrator reads NO template and NO live source. It reads `refresh-scope.sh` output, `diff -u` hunks, and worker reports.
- A worker reads at most **2 files**: its one template and its one live source. Nothing else — not a sibling template, not the map, not a reference doc beyond what its brief quotes.
- One worker owns exactly one template pair.

### Step 1 — Scope

```bash
bash scripts/refresh-scope.sh scan {live-project-root}
```

`CHANGED` is the work list. `UNCHANGED` is a mechanical untouched-proof — skipped, never re-read. A scan that fails to RUN is a failed look, not an empty one: stop and report it.

`MISSING-SOURCE` exits 3 and blocks the pass → Step 2. `curated` templates have no live source and are never derived here.

### Step 2 — `rulings`

Runs alone as `/pfm:refresh {root} rulings`, and runs first whenever a scan exits 3. Every `MISSING-SOURCE` gets one of three rulings, and each is a judgment the user's blueprint has to live with, so state the evidence for each:

- **REMAP** — the live source moved or was renamed. Point the entry at the successor. Prove the successor is the same file (same role, continuous content), never a same-named coincidence.
- **DELETE** — the live source is gone with no successor and the template ships a dead pattern. Remove the template file AND its map entry, end to end, including any pointer that cited it.
- **CURATED** — the template has legitimately outgrown its live source and is now hand-maintained here (every machine-global template is this by law: `templates/global/**` IS the truth, so a global entry still carrying a live source is a mapping bug). Set `curated: true` and drop the dead `sources` map.

`UNMAPPED-LIVE` gets a mapping or an `ignore_sources` entry, same evidence bar.

Re-baselining around a missing source keeps a zombie template alive forever — never regen to clear one.

Two integrity checks the scan structurally cannot make, because it reads the map's keys rather than the tree:

```bash
# ZOMBIE — map entry whose template file does not ship
jq -r '.templates | keys[]' templates/refresh-map.json | while read -r k; do [ -e "templates/$k" ] || echo "ZOMBIE $k"; done
# ORPHAN — shipped template with no map entry
comm -13 <(jq -r '.templates | keys[]' templates/refresh-map.json | sort) \
         <(cd templates && find . -type f -not -name refresh-map.json | sed 's|^\./||' | sort)
```

### Step 3 — Cascades first, then batch

A per-template worker structurally cannot carry a change that spans templates: it sees one file, so it applies the cascade's local fragment and leaves the blueprint half-migrated — some files on the new contract, one on the old, every check still green. Detect these BEFORE batching and route them out of the per-template lane.

A hunk is a CASCADE when it renames or retires a token, path var, command, agent, artifact, or term that other templates name. Measure the blast radius, closed-world, before ruling it:

```bash
grep -rl '{the symbol}' templates/ | sort        # every template that must move together
```

Each cascade becomes ONE task owning every file in its radius, dispatched alone, verified by the symbol's count reaching zero (or its full replacement) across `templates/`, `docs/`, and every citing pointer. A cascade half-applied is a worse defect than one not started, so a batch worker that meets a cascade hunk reports it and applies nothing.

The reverse holds too: a term this blueprint's other files depend on is not renamed because one live file renamed it. Grep the term before accepting a rename — a live-side rename with citers here is LOCAL until its own cascade task lands.

Order the remaining `CHANGED` list smallest diff first (`diff -u {template} {live} | grep -c '^[+-]'`) so the cheap batches retire early and the expensive ones arrive with the classification pattern already established. Batch at `--batch N` (default 4), one worker per template.

Dispatch each batch as ONE message — every sibling in a wave goes together, and a missing report is a named coverage hole, never a silent one.

Tier and effort per the fleet prompt § Model Selection: **spec-execution (sonnet), effort High**. The work arrives with a spec; the judgment that stays here is which hunks were classified wrong.

#### The worker brief

Every dispatch carries all five briefing fields (root `CLAUDE.md` § Subagent dispatch), plus one input the 2-file cap makes it impossible for a worker to fetch:

**Quote the source project's commit messages for this live file into the brief** — `git -C {live-root} log --format='%h %s%n%b' {last-sync}.. -- {source}`, where `{last-sync}` is the `Source:` trailer of the newest `release:` commit on `main`. Those messages are where that project ALREADY ruled the change framework-bound, and a worker that cannot see them will read a generic mechanism as install-specific topology and rule it LOCAL. A file with no commits since `{last-sync}` is briefed as such, so "no message quoted" means the orchestrator looked, not that it skipped.

> Re-derive ONE blueprint template from its live source. Read at most these 2 files: the template `templates/{key}` and the live source `{live-root}/{source}`. Read nothing else.
>
> 1. Stage and run the deterministic pass first: copy the LIVE source to a scratch path, `bash scripts/genericize.sh -i {scratch}`. It applies `scripts/placeholder-map.tsv` longest-search-first. Never hand-substitute a value that map already covers.
> 2. `diff -u templates/{key} {scratch}` — the hunks are the whole job.
> 3. Classify EVERY hunk as exactly one of:
>    - **SYNC** — a framework change: a mechanism, rule, gate, threshold, structure, or correction any adopter of this blueprint would want. Apply it to the template.
>    - **LOCAL** — project-specific: the source project's brand, roster, ports, stack, domain nouns, its own business rules, a fix meaningful only in that repo. Never applied. A LOCAL hunk that carries a value the template already parameterizes is TOKEN instead. A mechanism is not LOCAL merely because the source project is its only instance today — judge the mechanism, and treat the concrete repo, remote, or path it names as the TOKEN half. A brief quoting a ledger bullet for the hunk has already settled it as SYNC.
>    - **TOKEN** — a project value sitting where the template holds a registered placeholder. Apply with the token, never the literal. One canonical token per concept; a concept with no registered token is UNRULED, not an invented one.
>    - **UNRULED** — you cannot tell. Report it verbatim with your reasoning. An unruled hunk is a result; a silently dropped one is a defect.
> 4. Apply only SYNC and TOKEN, surgically, with `Edit`. A template IS the live source file verbatim — same structure, mechanics, character, logic; only project-specific values swap for tokens. Never abstract, skeletonize, or thin prose, and never trim a persona's voice sections.
> 5. Verify: `bash scripts/leak-check.sh --files templates/{key}` and quote its exit status. No machine-absolute path (`/home/…`, `/Users/…`), no brand current or former, no PII.
>
> Return: one line per hunk (`SYNC` / `LOCAL` / `TOKEN` / `UNRULED` + a phrase naming it), the leak-check exit status quoted, and one draft release-note bullet for the SYNC set (`- {Tier}: {scope} — {semantic change}`) for the release reviewers to weigh. Report a tool that would not run as a failure naming the tool — never as a clean result.

### Step 4 — Review, then continue

The orchestrator reviews each batch before the next dispatches. Agent reports are evidence, not truth: read the actual diff, never the worker's claim about it.

```bash
git diff --stat templates/            # scope: only briefed templates moved
git diff templates/{key}              # the hunks that landed
bash scripts/leak-check.sh --files $(git diff --name-only templates/)
```

Reject and re-dispatch on any of: a template touched that no brief named, a LOCAL hunk applied, a literal where a registered token belongs, prose thinned rather than derived, a leak-check the worker did not quote. A worker's UNRULED hunks are ruled HERE, by the orchestrator or by the user — never left in the report.

Reconcile telemetry per batch: workers dispatched vs reports received, and the count appears in the report.

### Step 5 — Close

1. `bash scripts/refresh-scope.sh regen {live-project-root}` — fresh hashes are the next release's baseline. Only after every ruling from Step 2 has landed; regen over an unruled MISSING-SOURCE re-baselines a zombie.
2. A SYNC set needs no ledger — the release reviewers write its note from the diff.
3. Report: templates re-derived / skipped-unchanged / ruled, the per-verdict hunk totals, every UNRULED hunk and how it was ruled, workers dispatched vs reports received, leak-check status, and what a reader must verify by hand.

### Rules

- `--dry-run` classifies and reports; it writes no template, no map entry, and no drift line.
- Never regen hashes for a template a worker did not actually re-derive — the baseline would claim a sync that never happened.
- A worker that reports zero hunks names the command it ran; "found nothing" and "failed to look" are different results and are reported differently.
- The public repo is the stakes: a leaked identifier cannot be unpublished. Leak-check is the backstop, never the plan.
