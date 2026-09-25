# SETUP — Installing Professor

Run inside your target project after `pfm init`. The command scaffolds project templates once, with tokens intact and per-file baseline pins. Claude reads this file, conducts the install interview, and adapts those local files in place. Result: a `.claude/` that reads like it was written for your project, because it was.

---

## Contents

- [Prerequisites](#prerequisites)
- [How to install](#how-to-install)
- [Install interview](#install-interview)
- [Phase 1 — The interview](#phase-1--the-interview)
- [Phase 2 — Customization](#phase-2--customization)
- [Phase 3 — Smoke test](#phase-3--smoke-test)
- [Phase 4 — Memory backup](#phase-4--memory-backup-optional-opt-in)
- [Common gotchas](#common-gotchas)
- [After install](#after-install)
- [Staying current](#staying-current)

---

## Prerequisites

- Git repository (at least one commit on `main` or `master`)
- Claude Code CLI installed and configured
- `pfm` installed with a permanent tagged blueprint clone recorded by `pfm install --yes`
- 10 minutes for the interview

---

## How to install

**The fastest path:** scaffold once, then let Claude conduct the interview.

```bash
# Inside YOUR project
cd /path/to/your-project
pfm init .
claude
# Tell Claude: follow the printed blueprint docs/SETUP.md § Install interview.
```

`pfm init` prints the exact permanent blueprint `docs/SETUP.md` path to follow. Keep that clone: updates, template diffs, and the deep-rr engine read it.

Claude runs Phase 1 (questions), Phase 2 (local adaptation), then Phase 3 (smoke test). You answer about 10 questions. Claude does the rest.

**The manual path:** run `pfm init`, read `BLUEPRINT.md` and `PLACEHOLDERS.md`, then adapt the scaffolded local files and pin any additional template mappings by hand. Slower but doable.

---

## Install interview

This section is the runnable handoff from `pfm init`; no install slash command exists. Work only in the target project. The blueprint clone is read-only input.

1. Read this file and `docs/PLACEHOLDERS.md` from the blueprint path printed by `pfm init`. Conduct every applicable Phase 1 question below and show the complete local write plan. Wait for the user to type **"go"**.
2. Fill registered tokens directly in the scaffolded local files. These files are now the project's source of truth; do not regenerate them from templates or rewrite `.professor/baseline.json`. Files skipped by `pfm init` as `CONFLICT` stay unpinned and unchanged unless the user explicitly includes them in the plan.
3. Materialize the roster-only sources that `pfm init` deliberately skips:
   - `templates/project/per-project/CLAUDE.md` → `{project}/CLAUDE.md` for each child project that needs one.
   - `templates/project/commands/per-project/testing-manual.md` → `.claude/commands/{project}-testing-manual.md`, one per roster entry (`testing-manual.md` for a single-project install), its eleven sections filled from that project's real test layout and commands.
4. Write `.professor/manifest.json` with the confirmed answers in its `interview` object, including `blueprint_clone_path`, roster, tool commands, ports, optional roles, and engine choices — `pfm init` writes only `.professor/baseline.json`, so the interview creates `manifest.json` in the format step 11 shows; on a re-run, preserve every non-interview field. `manifest.json` is the user-owned install record; `.professor/baseline.json` remains pfm-owned provenance.
5. Verify that no required token remains, every generated agent reference resolves, and scripts preserve executable mode. If Codex is enabled, run `pfm codex build` and `pfm codex check` from the target project after the local Claude files are final.
6. **Close by pinning every file deployed by the interview.** For each local created from a roster-only template, run:

   ```bash
   pfm update pin --template project/commands/per-project/testing-manual.md <local>
   ```

   Use the matching template path for `project/per-project/CLAUDE.md` and any other interview-deployed template. Many local files may pin the same template. Do not re-pin files scaffolded by `pfm init`; their template-byte pins already exist and local token filling does not invalidate them.

---

## Phase 1 — The interview

Claude (in your target project) asks these questions, in this order. Answer them however you want — short, long, with examples. Claude will turn them into template parameters.

### 1. Project identity

> What does your project do, in one sentence?

This becomes `{PROJECT_NAME}` and `{PROJECT_PITCH}`. Example: "Acme is a developer-tooling platform that watches CI runs and assists the engineer."

### 2. Character name & voice (MANDATORY — cannot be skipped)

> Default character is **Professor** — grandfatherly polymath with 15+ PhDs, one in whatever area the work touches. Warm, precise, gently devastating. Cross-disciplinary lens. Takes life easy but not too easy. Pick: keep Professor, rename (voice stays), or supply a custom voice (3–6 tone keywords + a one-line vibe). You MUST land on one — the persona section is load-bearing infrastructure, not optional flavor.

Most adopters keep Professor as-is. The voice transplants well across domains. If you want a different name (e.g., "Beatrix" for a finance project, "Gandalf" for an open-source library), name it. The voice can stay.

The Professor (`professor.md`) ships as ONE version: the **session style** loaded on every main-loop turn.

Sacred ground (the topics where humor drops) is collected in question 8 — it feeds the Tier B archetypes; the Professor persona itself stays generic.

### 3. Project roster

> How many projects does this repo hold, and what is each one? **One project is valid and first-class** — a single-project repo is a roster of one, not a stripped-down path. For each project, give: directory, role (what it does), tech stack, package manager, test runner, build tool, and dev server port(s).

This becomes the **roster** — the ordered list of 1..N projects that drives the whole install. Templates carry generic per-project PATTERN blocks; Phase 2 expands each block once per roster entry (see "Materialization" below), so a 1-project and a 7-project repo get correctly-sized files from the same source. **The blueprint assumes no fixed project count** — whatever you list here is the truth.

For each roster entry, Claude needs:

- Directory name (you choose; for a roster of one this is the repo root itself)
- Role label (your own words — one line saying what it is; the blueprint has no role names of its own)
- Tech: language, framework, package manager, test runner, build tool, dev server port(s)
- Ownership facts, each answered by naming at most one entry: which owns the shared infra/orchestration (`Makefile`, containers, DB, queue); which owns the migrations dir; which holds the LLM-calling code, if any; which serves the public site, if any

**Single-project repo (roster of one):** the worktree is the repo root (no per-project subdir), there are no cross-project/integration steps, routing is trivially that one project, and the multi-project framing collapses to "the project." Skip child `CLAUDE.md` files (nothing to consolidate). Any project agent lives flat at `.claude/agents/`; a flight's executors and lander are machine-global and picked by the index row's `rating`, with no per-project fan-out.

**Multi-project repo (roster of 2+):** the main-loop session designs cross-project contracts directly (no dedicated consolidation agent). For each entry, create `{project}/CLAUDE.md` and `{project}/.claude/agents/`.

Example rosters (two possible shapes — yours may have one entry or seven; the names are yours):

- Roster of one: `.` — the whole repo: Go, `go test`, port 8080; owns its own infra (`Makefile` + compose)
- Roster of three: `a` — the HTTP service: TypeScript, pnpm, vitest, port 3000 · `b` — the queue consumer: Python, uv, pytest, no port · `c` — owns the shared infra: `Makefile` + Docker Compose for PostgreSQL + Redis, no port

**Specialist agents:** the flights agents build and gate every project; add a specialist when a narrow concern justifies it:

| When to add one | What it owns |
| ---------------------------------------- | --------------------------------------------------- |
| Visual/interaction layer is non-trivial | Colors, typography, spacing, layout (`ui-ux`) |
| Schema/migration changes are risky | Data layer, migrations, seeding (`db-admin`) |
| Deployment configs are real code | Infra configs, environment promotion (`devops`) |
| Prompt engineering is its own discipline | Prompts, evals, knowledge ingestion (`ai-engineer`) |

A specialist is an executor like any other: the orchestration brief names the agent type that runs each task file.

### 4. Tech stack details

For each subproject, Claude pins these into the agents and scripts:

- Test command (`pnpm test`, `pytest`, `cargo test`, etc.)
- Lint command
- Typecheck command (if applicable)
- Build command
- Dev server start command
- Dependency install command (`pnpm install`, `uv sync`, `cargo build`, etc.)

These go into `worktree.sh`, `dev.sh`, and each project's testing manual.

### 5. (retired)

Question 5 (Professor's disciplines) is retired — the persona's qualification is fixed prose ("15+ PhDs, one in whatever area the work touches"), nothing to collect. The number is kept so `--re-interview N` stays stable for questions 6+.

### 6. (retired)

Question 6 (Council panel) is retired because no Council command ships. Omit `council_panel` from the manifest. The number is kept so later question references stay stable.

### 7. Tier B opt-ins

For each Tier B archetype, opt in or skip. For each opt-in, fill in the placeholders.

#### `/officer` — compliance enforcer

> Do you have regulatory exposure? GDPR, HIPAA, FDA, SOC2, ISO 27001, MiFID, export controls, supply-chain rules, financial reporting?

If yes, fill in:

- `{REGULATION}` — the framework name(s)
- `{ENFORCEMENT_AUTHORITY}` — the body that enforces
- `{DATA_SUBJECT_RIGHTS}` — the rights framework
- `{INCIDENT_NOTIFICATION_TIMELINE}` — your breach-notification deadline

If no, skip — most projects don't need this.

#### `/mentor` — business advisor

> Is this a commercial venture? Do you need NL/US/UK/etc. company formation, funding, GTM, regulatory cost/benefit advice?

If yes, fill in:

- `{MARKET_SEGMENT}` — your market
- `{JURISDICTION}` — country + regions
- `{LEGAL_ENTITY_TYPE}` — local entity type (BV, LLC, GmbH, Ltd, etc.)
- `{FUNDING_LANDSCAPE}` — VCs, angels, grants relevant to your space
- `{REGULATORY_BODIES}` — agencies/laws affecting business operations

If no (open-source, research, hobby), skip.

#### `/marketer` — visibility strategist

> Do you market this product, write content, attend conferences, or run sales/SEO?

If yes, fill in:

- `{CHANNEL_LANDSCAPE}` — channels your audience uses
- `{TARGET_LANGUAGE}` — primary marketing language (en, nl, de, ja, etc.)
- `{COMPETITIVE_LANDSCAPE}` — named competitors
- `{INDUSTRY_CONFERENCES}` — events that matter

If no, skip.

### 7b. Codex dual-runtime (OPTIONAL)

> Do you also use OpenAI Codex? (Everything works without it — this adds a second runtime for cheaper implementation.)

If yes: the interview creates `.codex/` as a pointer layer over the project's local Claude sources, then runs `pfm codex build` and `pfm codex check`. `AGENTS.md`, command and skill pointers, and `.codex/agents/*.toml` are generated; `config.toml` and `rules/*.rules` keep their hand-written portions. Claude and Codex read the same Professor contract; the pointer layer translates mechanics, not identity. Either runtime can orchestrate when invoked with the matching command surface.

If no: skip — the entire Codex layer is omitted. No pipeline operation requires it.

### 8. Sacred ground

> What does "do no harm" mean in your domain? Privacy, safety, correctness, financial integrity, narrative coherence, scientific reproducibility, security?

This becomes `{SACRED_GROUND}` and is referenced by:

- Officer (if opted in — the protected category)

Be specific. "Privacy" is too vague. "Patient session content and identifying details" is concrete. "Financial transaction integrity at the millisecond level" is concrete. "Scientific data reproducibility for FDA submissions" is concrete.

### 9. Port allocation

> What port ranges are free on your dev machine?

Claude pins them into `alloc-ports.sh` as `{PORT_DEFAULTS}` — one range per roster entry that serves a port (something like 3000-3099 for the first, 8080-8179 for the next), plus 5432-5531 for postgres, etc. — adjust to whatever's free.

### 10. Confirmation

Claude shows you a summary of all answers + a list of files that will be written. You confirm or edit. Then Phase 2 begins.

---

## Phase 2 — Customization

> **Materialization — how the roster expands.** Several templates carry per-project **PATTERN blocks**, written once with generic `{project}` tokens: the per-project testing manuals `.claude/commands/{project}-testing-manual.md` (plus any specialist agents), and the `PROJECTS=(…)` arrays in `worktree.sh`/`dev.sh`. For each roster entry, Claude **expands every pattern block once**, substituting that entry's directory, role, stack, package manager, test runner, and port — then fills the `PROJECTS=()` arrays so the scripts iterate the real roster. A roster of one expands each block once; a roster of seven expands it seven times. **Never carry a pattern block for a project the roster does not list** — no installed file may reference a testing manual or an agent for a project that does not exist, and no `{project}` placeholder may remain unexpanded. **Single-project install:** the worktree is the repo root, the `PROJECTS=()` array holds the single entry (or the scripts drop the loop entirely), and cross-project consolidation steps are omitted.

Claude takes your answers and:

1. **Writes root `CLAUDE.md`** — fills in `{PROJECT_NAME}`, `{PROJECT_PITCH}`, the Professor persona section, and the non-negotiable rules. Emits `{PROJECT_ROSTER}` (one Architecture bullet per roster entry); a single-project install collapses the multi-project framing to "the project." Strict-typing and infra rules emitted per roster entry (one typing rule per typed stack; the infra rule only if a roster entry owns infra, with that entry's directory as `{PROJECT}` in `make -C {PROJECT}`).
2. **Writes per-project `CLAUDE.md` files** (roster of 2+) — one per entry, with that entry's tech stack and conventions. A roster of one has no child CLAUDE.md.
3. **Writes Tier A command files** — `/pcm`, `/dev`, `/rnd`, `/audit:*` (the machine-global `/flights:*`, `/pfm`, `/quality:*` arrive by `pfm install`). Voice intact, domain content filled.
4. **Writes Tier B command files** for each opt-in — `/officer`, `/mentor`, `/marketer`. Archetype skeletons with your placeholders filled. The leading `>`-quoted "Required placeholders (fill at install)" meta-block from each template is stripped before save — that block is install-time scaffolding, not runtime content. A correctly-installed Tier B command starts with the H1 heading and goes straight to the `$ARGUMENTS` line. A declined archetype that `pfm init` already scaffolded is deleted, its pin forgotten with `pfm update drop <local>`, then its template silenced with `pfm update ignore <template>`; see [Review and adopt upstream project changes](#review-and-adopt-upstream-project-changes).
5. **Writes root agents** — `gitter` always (`tracer`, `flights-speccer`, `flights-orchestrator`, `flights-mechanical-executor`, `flights-smart-executor`, `flights-lander`, `general-orchestrator`, `general-mechanical-executor`, `general-smart-executor`, `reviewer`, and `rr` are machine-global, linked into `~/.claude/agents/` by `pfm install`, never copied into the project). Cross-project consolidation for a roster of 2+ runs in the main-loop session, not a dedicated agent.
6. **Writes the per-project testing manuals** — for each roster entry, instantiates `.claude/commands/{project}-testing-manual.md` from `templates/project/commands/per-project/testing-manual.md` with that project's tiers, test homes, run commands, gates and traps; the flights agents read it, so no per-project `developer` or `qa` agent ships. Specialists from Q3 are written under `{project}/.claude/agents/` only when the project wants them; no shipped command spawns one by name.
7. **Writes scripts** — `worktree.sh`, `alloc-ports.sh`, `dev.sh`. Fills the `PROJECTS=(…)` arrays in `worktree.sh`/`dev.sh` from the roster so they iterate the real entries, with each entry's setup logic and port ranges pinned. A single-project roster fills the array with one entry (or drops the loop). 7a. **Installs skills.** The blueprint bundles the attributed `legal` reference shelf under `templates/project/skills/legal/`; every registry skill is **source-fetched** from its canonical public repo (listed in `templates/project/skills/sources.json`) into `.claude/skills/{name}/`, so those external skills cannot silently drift inside the blueprint. The installer copies the bundled shelf, clones each registry skill, parameterizes where needed, and removes each clone's `.git/` directory so the installed skills are plain files. The reasoning protocols that once shipped as bundled skills — `/rnd`, `/quality:prompt`, `/quality:doc`, `/audit:code-hygiene`, `/audit:security` — are **commands**. Project-specific commands live under `templates/project/commands/`; shared commands live under `templates/global/commands/`, and machine-global skill directories under `templates/global/skills/` — both linked by host installation. `/rnd` is project-scope: the command owns the RND lifecycle and executes its own run. The table records each subject's source path and its parameterization.

| Skill / command | Source | Parameterization |
| --------------------- | --------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `legal` | Bundled `templates/project/skills/legal/` | None |
| `deep-rr` | in-tree at `{BLUEPRINT_CLONE_PATH}/workflows/deep-rr/` — ships with the blueprint clone, no separate fetch | None |
| `ghostwriter` | host-global source-fetched (`templates/global/skills/sources.json`) <https://github.com/rezzminator/ghost-writer> | None |
| `vision-factory` | host-global source-fetched (`templates/global/skills/sources.json`) <https://github.com/rezzminator/vision-factory> | None |
| `/rnd` | Command `templates/project/commands/rnd.md` | Replace `{PROJECT}` (the entry holding the LLM-calling code), `{AI_SERVICE_NAME}`, `{ai_module}`, `{LLM_PROVIDER}`, `{SECONDARY_LANG}` |
| `/flights:*` | Commands `templates/global/commands/flights/*.md` — host-global, linked by `pfm install` | None (pipeline-coupled) |
| `/quality:prompt` | Command `templates/global/commands/quality/prompt.md` | Replace `{DOMAIN_ADJ}`, `{SENSITIVE_DATA}` |
| `/quality:doc` | Command `templates/global/commands/quality/doc.md` | Replace `{DATABASE}`, `{ORM}`, `{API_PROTOCOL}` in examples |
| `/quality:description` | Command `templates/global/commands/quality/description.md` | None |
| `/quality:md-forlint` | Command `templates/global/commands/quality/md-forlint.md`, config `templates/project/rumdl-policy.toml` → adopter `.rumdl.toml` | None |
| `/quality:llm-codebase`, `/quality:integration-suite` | Commands `templates/global/commands/quality/{llm-codebase,integration-suite}.md` — host-global, linked by `pfm install` | None |
| `/audit:code-hygiene` | Command `templates/project/commands/audit/code-hygiene.md` | Hydrated by RR (Phase 2.5) |
| `/audit:security` | Command `templates/project/commands/audit/security.md` | Hydrated by RR (Phase 2.5) |

7b. **Installs statusline** — obtains the `pfm` binary and runs `pfm install --yes`, which merges a `statusLine` command running `~/.local/bin/pfm statusline` into `~/.claude/settings.json`. The native renderer shows model, fleet counts, context, git, cache state, cost, spend, and rate limits; detached refreshers keep network work off the render path.

7b-i. **Installs global settings** — merge `templates/project/settings-global.json`'s keys into the adopter's OWN `~/.claude/settings.json`. **Merge, never overwrite**: this is the adopter's personal, machine-wide config file — it almost certainly already carries their own `model`, `theme`, MCP permissions, and hooks, and a blind copy would destroy them. Do a key-by-key JSON merge (add/update only the keys this template ships), the same discipline as step 7b's statusline block. The template currently ships exactly one key:

    ```json
    { "cleanupPeriodDays": 36500 }
    ```

    `cleanupPeriodDays` controls Claude Code's startup sweep: every session transcript AND every orphaned git worktree older than this many days is deleted automatically, with no warning, before you ever see them. The stock default is `30` — leave a project untouched for a month and its chat history and worktrees are gone at the next launch. `36500` (100 years) is the practical "off" value. **`0` is not "off"** — it fails settings validation (the minimum accepted value is `1`), so never write `0` trying to disable the sweep. This key is global: it governs every project on the adopter's machine, not just this one, so the merge (not overwrite) discipline matters even more here than for a project-scoped file.

7c. **Configures markdown auto-formatter** — `format-md.sh` hooks into Claude Code's `PostToolUse` event for `Edit` and `Write` tools. When Claude edits a Professor-owned `.md` file (CLAUDE.md, `.claude/`, `docs/commands/`, `docs/agents/`, `docs/epics/`, `docs/dev/`, `docs/business/`, or child project CLAUDE.md files), `rumdl` formats it under the repo-root `.rumdl.toml` policy (`/quality:md-forlint`). Non-Professor files and generated mirrors are ignored. Add to `.claude/settings.json`:

    ```json
    {
      "hooks": {
        "PostToolUse": [
          {
            "matcher": "Edit|Write",
            "hooks": [
              {
                "type": "command",
                "command": "/absolute/path/to/your-project/.claude/scripts/format-md.sh"
              }
            ]
          }
        ]
      }
    }
    ```

    Requires `jq` and `rumdl` (provisioned by `pfm install`). A missing tool prints one stderr line naming it and leaves the file unformatted — never a silent skip.

7d. **(Opt-in) Installs multi-account fleet tooling** — obtain the versioned binary using [INSTALL.md](../INSTALL.md), then run `pfm install` to preview or `pfm install --yes` to apply. Installation stages embedded host assets, links the global command/agent registry from the recorded source clone, wires configured account settings and hooks, installs the shell launcher, and configures the platform scheduler (systemd on Linux, launchd on macOS). Optional MCP and the Professor VS Code extension (Professor's assistant in VS Code) — registered in each product's own extension index, visible as **Professor** in the Extensions view and a **Professor** entry in the terminal `+` dropdown, with the `PFM` terminal made default — retain their explicit enablement. Press Ctrl+Shift+Alt+T (macOS: Cmd+Shift+Alt+T), run **Professor: New Chat Terminal**, or pick **Professor** from the terminal `+` dropdown — all three give the next icon and colour; the default `+` terminal is `PFM`. Claude uses the selected harness replacement; configured Codex accounts receive the trusted native SessionStart appendix hook. An empty engine roster stays empty. Existing custom settings and hooks are preserved; owned predecessors are migrated with backups. The write gate refuses a currently running name-sync job, while an available idle manager is supported. Service activation errors remain errors. `pfm uninstall` removes owned registrations and staged assets. Skipped if the user declines.

7d-i. **(Opt-in, host-level) The professor MCP server** — `pfm install` registers one MCP server, `professor`, while either of its two opt-in families is enabled (`mcp.servers.chat.enabled` for chat, `harvester.enabled` for the harvester) and removes its owned `professor` entries when both are off. Every engine registers the same stdio command, `~/.local/bin/pfm mcp serve --stdio` with the absolute binary path, which forwards to the daemon's `/mcp/professor` and serves in process when the daemon is absent or incompatible: a `mcpServers.professor` entry in each configured Claude user registry (`~/.claude.json` for the implicit account, `<CLAUDE_CONFIG_DIR>/.claude.json` for explicit accounts), one installer-owned `mcp_servers` fence holding `[mcp_servers.professor]` (`command`, `args`) in each configured Codex home, an `mcp.professor` entry of type `local` in OpenCode's `opencode.jsonc`, and an exact ownership record under the managed root. pfm's earlier `chat` and `harvester` entries are removed in every registry, config and home. Manual registrations and unrelated private configuration are preserved; failed writes retain recovery receipts. Doctor inspects these actual client paths and the historical root `.mcp.json` migration evidence. The chat family exposes the fleet as `chat_*` tools — ls, new, open, status, last, find, read, save, capture, keys, name, inject, self-compact, kill, unkill, resolve, whoami — which the agent calls directly, plus `servicedesk`; the harvester family exposes the `harvester_*` tools. The chat family replaced the host-level `/chat:*` slash-command family, removed in v0.62.0; `/reload` remains available alongside the shared global command registry.

7e. **Probes host tooling (git-host bridge)** — checks the install machine for `gh` and `glab` (`command -v`). For each present, writes a one-file index skill at `.claude/skills/host-{gh|glab}/SKILL.md` whose `description` records that the CLI is available on this host for {GitHub|GitLab} operations. It carries no procedure — it is the bridge that tells the Professor which CLI to drive: a GitLab adopter forks + releases through `glab`, a GitHub adopter through `gh`. Machine-specific, so it is generated per install (re-run on each machine), never shipped as a template. Absent tools get no skill.

7f. **Installs themes** — places each Claude Code theme listed in `templates/themes/sources.json` into `~/.claude/themes/`: `source_fetched` entries are fetched from their canonical public repo (the blueprint never vendors a copy, so it can't drift); `bundled` entries ship beside the manifest — today three overlays merged onto the fetched Tokyo Night, `professor-gold` / `professor-silver` / `professor-bronze`, one per fleet account medal (🥇🥈🥉), each changing only the input bar (`promptBorder` + `promptBorderShimmer`) — selected per account with `"theme": "custom:professor-gold"` in that account's `settings.json`. For `tokyo-night`: `mkdir -p ~/.claude/themes && curl -fsSL https://raw.githubusercontent.com/rezzminator/claude-code-tokyo-night/main/tokyo-night.json -o ~/.claude/themes/tokyo-night.json`. Activate with `/theme` → "Tokyo Night" (requires Claude Code v2.1.118+). Themes install to the user's home, so they are shared across all the user's projects. To match the terminal's own base background to the theme (VS Code `terminal.background`, or the profile background in iTerm2/Apple Terminal/Ghostty/Kitty/WezTerm), follow the theme repo README: <https://github.com/rezzminator/claude-code-tokyo-night#match-your-terminal-background-optional>.

8. **Creates directory structure** — `docs/agents/`, `docs/commands/`, `docs/dev/tasks/`, `docs/dev/tasks/archive/`, `.worktrees/` (gitignored). Flight directories live in pfm's state directory: `$HOME/.local/state/pfm/flights/{project}/{flight}/` is created by the run itself, outside the repo entirely, and kept across reboots.

8a. **Installs command reference docs** — copies `templates/project/docs-commands/` into `docs/commands/` verbatim; the template tree mirrors `$CDOCS` exactly (e.g. `docs-commands/build/references/qa-commons.md` → `docs/commands/build/references/qa-commons.md`), so commands that cite a reference doc find it on disk.

8b. **(If Codex opted in)** Creates `.codex/` as a pointer layer over `.claude/` — never a restatement of it. Writes `config.toml` (sandbox reach + the `{CODEX_MODEL}`/`{CODEX_REASONING_EFFORT}` pins) and `rules/repo-law.rules` (the execpolicy door lock for non-gitter roles). Runs `pfm codex build` to compile every root and per-project Claude source into the Codex mirrors, then `pfm codex check` to verify them. Registry changes require a new or reloaded Codex session. If Codex was NOT opted in, this step is skipped entirely.

9. **Updates `.gitignore`** — adds `.worktrees/`, `tmp/`.
10. **Maintains `.professor/` state** — `manifest.json` holds the user-owned interview answers, while `baseline.json` holds pfm-owned per-file template pins.
11. **Writes `.professor/manifest.json`** — created here (`pfm init` leaves only `baseline.json`); on a re-run replace its `interview` object with the confirmed answers while preserving every non-interview field. Format:

**Build roster validation:** no installed file is allowed to carry blueprint example projects that the target repo does not have. The installer must generate testing manuals (and any specialist blocks) only for roster entries, fail if any `{project}` pattern token remains unexpanded, and then verify every referenced `*/.claude/agents/*.md` path exists. A roster of `a` and `b` leaves no block for a `c` the repo does not have.

```json
{
  "schema": 1,
  "version": "0.5.0",
  "installed_from_tag": "v0.5.0",
  "installed_at": "2026-04-28T14:32:00Z",
  "updated_at": null,
  "interview": {
    "project_name": "neurolab",
    "project_pitch": "AI-assisted neuropsychological assessment platform",
    "character_name": "Professor",
    "character_voice": "keep",
    "sacred_ground": "patient cognitive assessment data and diagnostic accuracy",
    "structure": "multi-project",
    "subprojects": [
      { "dir": "a", "desc": "the assessment service", "pkg": "pnpm" },
      { "dir": "b", "desc": "the scoring worker", "pkg": "uv" }
    ],
    "tech_commands": {
      "a": {
        "test": "pnpm test",
        "lint": "pnpm lint",
        "typecheck": "pnpm tsc --noEmit",
        "build": "pnpm build",
        "dev": "pnpm dev"
      },
      "b": {
        "test": "uv run pytest",
        "lint": "uv run ruff check",
        "typecheck": "uv run mypy",
        "build": "skip",
        "dev": "uv run python -m worker"
      }
    },
    "tier_b": {
      "officer": {
        "enabled": true,
        "regulation": "HIPAA",
        "authority": "HHS OCR",
        "rights": "HIPAA Privacy Rule",
        "notification": "60 days"
      },
      "mentor": {
        "enabled": true,
        "market": "clinical neuropsych SaaS",
        "jurisdiction": "US",
        "entity": "LLC",
        "funding": "NIH SBIR, health-tech VCs",
        "bodies": "FDA (if SaMD), state licensing boards"
      },
      "marketer": { "enabled": false }
    },
    "codex": false,
    "ports": { "a": 3000, "db": 5432 }
  }
}
```

The `interview` field records the choices needed to understand the install; it is not an instruction to regenerate project files. Per-file upstream comparison state lives only in `.professor/baseline.json`, whose hashes cover template bytes with tokens intact.

### 2.7 Documentation scaffold (`docs/agents/`)

The main-loop session and every executor read a documentation hub that must exist on disk, or their references dangle. Seed it from the shipped skeletons:

1. Copy `templates/project/docs-agents/_index.md` → `docs/agents/_index.md` and `templates/project/docs-agents/standards.md` → `docs/agents/standards.md`, substituting `{PROJECT_NAME}` and roster tokens like every other template.
2. **If the project has enough code to document** → the main-loop session builds the clusters (architecture, api, map, features) from the codebase under `/quality:doc`, each with its own `_index.md`, and runs the Approval gate over each.
3. **If the project is too new** (no code yet, or the adopter skipped stack details) → defer, mirroring the empty-skill hydration pattern: keep the seeded hub + standards skeleton, leave the cluster rows pointing at to-be-created indexes, and note that the main-loop session fills them when the codebase exists. The hub must never reference a cluster file that is absent without marking it deferred.

---

## Phase 3 — Smoke test

After install, Claude verifies the project routes through the installed developer gate:

```
/dev status
```

Then exercise one small task through the pipeline and watch its project checks. The first run reveals anything missed in adaptation. If something asks the wrong question or runs the wrong command, invoke `/pcm` to fix it at the source.

Before creating the `professor: install` commit, close the update ledger per [Review and adopt upstream project changes](#review-and-adopt-upstream-project-changes): run `pfm update check`; run `pfm update pin --template <template> <local>` for every interview-deployed file, including per-project agents and each child `CLAUDE.md`; and run `pfm update ignore <template>` for every declined or deliberately non-materialized template — Tier B archetypes not opted in, `project/per-project/CLAUDE.md` for a roster of one, and `project/settings-global.json` because it is merged into home settings rather than installed as a project file. `pfm update ignore` refuses a template a local file is still pinned to: for a declined template `pfm init` scaffolded (the Tier B archetypes), delete the local file and run `pfm update drop <local>` first, then `pfm update ignore <template>`. `pfm update drop` alone is never how a template is declined — it forgets the pin, and the next check reports the template as `NEW` again. Re-run `pfm update check`; it must exit 0 and end in `clean` before the commit.

---

## Phase 4 — Memory backup (optional, opt-in)

Claude tells the adopter what this is, then ASKS whether to set it up — it's opt-in, never automatic.

> **Memory backup** points Claude Code's persistent project memory at ONE private git vault — every project in its own subdirectory — and auto-syncs it. A `SessionStart` hook auto-wires whatever project you open; a `SessionEnd` hook syncs the whole vault. So a machine wipe or a new machine doesn't lose what Claude has learned, across all your projects. Plain git: ~1 second, zero tokens. Set it up now?

If the adopter says **no**, skip the rest of this phase — nothing about the pipeline depends on it.

If **yes**, walk the procedure (full detail + every gotcha in `docs/references/memory-backup.md`):

1. **Create ONE PRIVATE vault repo** (e.g. `<gh-user>/<you>-memory`) on GitHub, and `git init` a local clone at the vault path — `$HOME/work/<vault-dir>` (the `{MEMORY_VAULT_DIR}` config point) or wherever `$CLAUDE_MEMORY_REPO` points. One vault holds every project's memory.
2. **Configure headless auth** — `gh auth setup-git` (registers `gh` as the credential helper; token in the OS keychain, HTTPS not SSH). Verify with `GIT_TERMINAL_PROMPT=0 git ls-remote origin HEAD` — it returns instantly, no prompt.
3. **Install the scripts + hooks.** Copy `templates/project/scripts/{memory-wire,memory-consolidate,memory-sync}.sh` to `~/.claude/scripts/` (substitute `{MEMORY_VAULT_DIR}`). These are user-level — they target `~/.claude/...` across every project, so they ship into `~/.claude/scripts/`, NOT a project's `.claude/`. Then add the `SessionStart` + `SessionEnd` hooks to global `~/.claude/settings.json`:

   ```json
   {
     "hooks": {
       "SessionStart": [
         {
           "matcher": "",
           "hooks": [
             {
               "type": "command",
               "command": "sh $HOME/.claude/scripts/memory-wire.sh"
             }
           ]
         }
       ],
       "SessionEnd": [
         {
           "matcher": "",
           "hooks": [
             {
               "type": "command",
               "command": "sh $HOME/.claude/scripts/memory-sync.sh"
             }
           ]
         }
       ]
     }
   }
   ```

   **Permission-mode pitfall:** editing global `~/.claude/settings.json` is a persistent, code-running config change — under auto-permission mode with `skipAutoPermissionPrompt`, the classifier SILENTLY DENIES it without prompting. So have the USER run this idempotent one-liner themselves (it won't duplicate or clobber existing hooks):

   ```
   python3 -c "import json,pathlib; p=pathlib.Path.home()/'.claude/settings.json'; d=json.loads(p.read_text()); h=d.setdefault('hooks',{}); h.setdefault('SessionStart',[]).append({'matcher':'','hooks':[{'type':'command','command':'sh \$HOME/.claude/scripts/memory-wire.sh'}]}); h.setdefault('SessionEnd',[]).append({'matcher':'','hooks':[{'type':'command','command':'sh \$HOME/.claude/scripts/memory-sync.sh'}]}); p.write_text(json.dumps(d,indent=2)); print('memory hooks added')"
   ```

4. **Run the consolidator once** — `sh ~/.claude/scripts/memory-consolidate.sh`. It migrates every existing `~/work/<project>` memory dir into its vault subdir (copy → verify file-for-file → swap for a symlink), skipping any dir already linked (including a legacy single-project root brain). New projects need no manual step — the `SessionStart` hook wires them on first open.
5. **Test with the test-payload trick.** A clean vault makes the sync a silent no-op — indistinguishable from "never fired" — so stage a deliberate pending change first (bait the hook). Exit cleanly with `/quit`, then confirm a new `pushed` line in `~/.claude/memory-sync.log` AND that the file reached the remote.

Tell the adopter to exit with `/quit` or `/clear` for a guaranteed synchronous flush; a hard window-close still works but leans on the script's self-heal to catch up next session. Full architecture, the single config point, the root-guard, multi-writer safety, all tips, and the new-machine restore steps live in `docs/references/memory-backup.md`.

---

## What if I want to add a Tier B archetype later?

You can opt in any Tier B archetype after install:

```
claude
> Add /officer to my Professor install. We're now subject to {REGULATION}.
```

Claude reads the blueprint's Tier B template for that archetype, runs the relevant subset of the interview, and copies + customizes the file. No reinstall needed.

Same for adding a new Tier A archetype if you build one — `/pcm` copies the template, you parameterize the content, done.

---

## Common gotchas

1. **Worktree script can't find your tools.** Make sure your shell environment is loaded inside the script — `source ~/.zshrc`, use absolute paths, or pin tool versions in a script-local `PATH`.
2. **Port allocation false positives.** `lsof -i :PORT` checks aren't always reliable across IPv4/IPv6 — adjust the script if you see false positives on your OS.
3. **Gitter tries to merge with conflicts unresolved.** That's a gap in your gitter setup; the template handles it, but if you simplified, restore the conflict-detection block.
4. **Agents writing to permanent docs.** Only the main-loop session should write to `docs/agents/` or `{project}/docs/`; a command-owned surface (`docs/business/**`) is written only by its owning command. If another agent tries, that's a `/pcm` fix at the source agent.
5. **`.worktrees/.ports` corrupted.** Manually edit; the format is one whitespace-separated line per pipeline.
6. **Character feels generic after install.** You probably stripped voice instead of parameterizing content. Voice is non-negotiable — adapt content, preserve character. Invoke `/pcm` and tell it which command lost its voice.

---

## After install

- Read `BLUEPRINT.md` § "The five load-bearing walls" — these don't change, ever.
- Verify the statusline shows in your terminal (you should see model, fleet counts, context %, and git branch). If not, check `~/.claude/settings.json` runs `~/.local/bin/pfm statusline` and that the binary is executable.
- Run `/flights:spec` for new features, then one of the `/flights:orchestrate-*` commands to fly it. Run `/pcm` to evolve the pipeline. Run the Professor analysis for cross-disciplinary analysis.

**When something feels wrong** after a few real pipelines:

- An agent always asks the same clarification → add it to the agent definition (via `/pcm`).
- A step always gets skipped → remove it or make it conditional (via `/pcm`).
- A bug class keeps recurring → add a non-negotiable rule to the relevant CLAUDE.md (via `/pcm`).
- A character feels off → describe what's missing to `/pcm` and let it edit the persona at the source.

The pipeline is supposed to evolve. Static configurations rot — evolving ones get sharper with use.

---

## Staying current

New Professor versions ship as semver git tags. Each tier stays current from its own source of truth:

| Tier | Truth | Mechanism |
| ----------------------------------------------------------- | ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Machine-global commands, agents, and skills | Blueprint originals | Symlink-live. `pfm update` advances the recorded tagged clone, rebuilds the binary, runs `pfm install --yes`, and refreshes registrations. |
| Project files (`CLAUDE.md`, `.claude/**`, docs, scripts) | The local file, full stop | Scaffolded once by `pfm init`. Upstream changes are reports to review and hand-apply; pfm never rewrites these files during update. |
| Engine mirrors (`AGENTS.md`, `.codex/**`, OpenCode outputs) | Generated from local project files | Never edit by hand. Run the owning compiler, including `pfm codex build` and `pfm codex check`, after changing its local sources. |

Before either tier moves, read every release note between the installed and the target version and merge their `#### → For:` actions — the rule lives in `INSTALL.md` § Updating.

### Review and adopt upstream project changes

0. An install that predates `pfm init` has no `.professor/baseline.json`: run `pfm update adopt` once inside it. It pins every mapped template whose local file exists, writes nothing else, and reports `adopted / kept / absent` counts. `--at <ref>` pins at the blueprint ref the install was last synced from, so the first `check` reports every template change since then instead of a false `clean`.
1. Run `pfm update check` inside the project. It only reads the blueprint, `.professor/baseline.json`, and local paths; it does not fetch, build, install, or write. Bare `pfm update` performs the machine update and then appends this project report when it finds a baseline.
2. Read every non-current row:
   - `UPDATED` — run the printed `git -C <blueprint> diff <pinned>..HEAD -- templates/<template>` command, then hand-apply the parts that belong in the local file.
   - `NEW` — adopt it only if useful, then map it with `pfm update pin --template <template> <local>`; a template this project will never take is silenced with `pfm update ignore <template>...` (`--undo` reverses; it counts as `ignored`, never as review).
   - `GONE-UPSTREAM` — keep the local file as yours and drop its pin, or delete it and drop the pin.
   - `LOCAL-DELETED` — restore the local file or drop its pin.
3. After reviewing and applying an `UPDATED` file, accept its new template baseline with `pfm update pin <local>`. Use `--all` only after every reported updated file has been reviewed and applied.
4. Re-run `pfm update check`; it exits `0` and ends in `clean` only when no item needs review. Rebuild opted-in engine mirrors from the resulting local source files.

The report is the update UI. There is no project regeneration, interview replay, automatic application, or three-way merge.
