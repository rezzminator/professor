# Blueprint compiler: `templates/**` is the only source; pfm compiles it through two middleware layers into every engine, per project and per machine

**Status:** SUPERSEDED (scope named in 2026-09-01-scaffold-and-own-update.md; engine registry, globallink, two-scope store stand as landed)

Refined: 2026-08-23 by Professor (user rulings § 2); round 2 2026-08-29 (amendments 9–15, verified against `main` @ `ff7460e`) · Project: pfm + templates + docs · Four milestones, one worktree, NO train (amendment 13) · Engine-contract prerequisite **satisfied on `main`**: `pfm/internal/engine/` exists with `func All()` and per-engine descriptors; this spec consumes that registry and never spells an engine name outside it.

## Amendments — 2026-08-28 (user-ruled; these override any matching text below)

1. **The shipped cards die, they don't shrink** (overrides Task 2.3.b's rewrite-in-place): the blueprint ships NO `ptm/update.md` and NO `ptm/release.md` — both already removed on `main` ahead of this train, together with the blueprint copy of `ptm/references/refresh.md` and their reference web (shipped `ptm.md` now carries § "Where a change lands"). Task 2.3.b proposed a fresh install-interview command; the adopter update flow is `pfm update`'s report read by the adopter's session — add a thin wrapper card only if M2 proves it necessary. Task 3.1.c's deletion list loses the two cards (already gone) and keeps the rest (refresh-map.json, refresh-scope.sh, the docs/ refresh.md original, build-codex.mjs).
2. **No hand-edited JSON** (amends ruling 7's container, not its substance — the override stays hand-written): override files are frontmatter-markdown — YAML frontmatter carrying `target`, `mode`, `headingPath`/`anchor`, `sourceHash`, `note`; the body IS the content, verbatim, no escaping. § 4.1's JSON schema maps field-for-field. `templates/templates.json` → `templates.toml`, `templates/placeholders.json` → `placeholders.toml`, `adapters/<engine>/engine.json` → `engine.toml` (§ 4.2–4.4 field-for-field; TOML for comments and reviewable diffs). Machine-written locks (`.professor/build.json`, `global-build.json`, `projects.json`) stay JSON.
3. **Routing law ships in prose too**: blueprint `ptm.md` § "Where a change lands" (on `main`) is the human-readable twin of § 3.5's markers; M3 sharpens its mechanics (override paths, `pfm professor build`) without changing its law.
4. **Overrides are tracked, never gitignored** (2026-08-29; amends ruling 2's mechanism, keeps its intent): `.professor/overrides/**` is customization and must survive — survival is guaranteed by `pfm update` and `pfm professor build` treating the overrides tree as **read-only input they never write into**, not by hiding it from git. The compiler never gitignores `.professor/overrides/`; § 3.6's stanza shrinks to `.professor/build.json` alone (the machine-written lock stays local); `manifest.overrides.tracked` is deleted from § 4.5 and Task 2.3.a writes no such key; § 3.6's tracked/untracked toggle logic dies with it.
5. **Global rosters ruled** (2026-08-29, user-ruled; sharpens Task 4.1's sets and overrides its census where they differ): DEAD, removed end to end with their reference webs: `p/slow-burn`, `sleep`, `goal-manager`, `animate` (+ `docs-commands/animate/`). GLOBAL commands: `/git`, the whole `wave/**` family (detokenized per Task 4.1's manifest-read recipe; `wave/walker-invariants.md` stays a PROJECT-scope data card — the engine and wave commands consume it per project), `h/**` (host-CLI bridges are host facts — machine scope, no longer KEEP-LOCAL), `/tokens` and `/rnd` (flattened out of `p/`, the `p:` namespace dropped), `/context-meter` (flattened out of `ptm/`), `quality/doc`, `quality/prompt`. Everything else stays project scope — including `/ptm` and `p/360` (overriding their Task 4.1 detokenize-candidate listing). GLOBAL skills (machine registry): `ghostwriter`, `vision-factory` (source-fetched per `sources.json`), `deep-rr` (original in-tree at `engines/deep-rr/`). `build-global-agents.py` is deleted — `pfm` (`codexgen/globalagents.go`) owns global-agent compile and registry linking.
6. **`/ptm` is renamed `/pfm`** (2026-08-29, user-ruled): every `/ptm` reference in this spec reads `/pfm` — the command card and its release flow, `ptm-guard.sh` → `pfm-guard.sh`, `docs/commands/ptm/` → `docs/commands/pfm/`, and the shipped `templates/project/commands/ptm.md` twin follow in one rename pass. The proposed install subcommand is superseded by `docs/SETUP.md` § Install interview; no slash command ships.
7. **The reviewer gates the merge** (2026-08-29, user-ruled): the global wave family adopts the reviewer-gate architecture — entry-point census → the global `reviewer` agent (its § REVIEW.md contract) → `REVIEW.md` findings ledger read by gitter from disk as the merge precondition; the walker demotes to an optional supplement where reachability is genuinely unmapped, never the gate. `gitter-phase-merge.md § 1` reads `REVIEW.md` instead of the old gate files.
8. **Global registration is a symlink, not a written copy** (2026-08-29, user-ruled; amends M4/§ 5.4's global-output mechanism): a global agent/command/skill's ORIGINAL lives in professor (this repo for the self-hosted install; the store for adopters); `pfm install` symlinks it into each engine's global registry (`~/.claude/agents/`, `~/.claude/commands/`, …), so the next update propagates through the link with no re-materialization. Engine-transformed twins (Codex `.toml`, OpenCode) are generated at RELEASE time and committed beside their original (the existing `agents/*.toml` pattern), then symlinked into their registries (`~/.codex/agents/`, `~/.config/opencode/agent/`). § 5.4.b demotion operates by removing the symlink; `global-build.json` records link targets, not content hashes. Global agent roster: tracer + rr (already global) + reviewer and scheduler (user-ruled 2026-08-29) + architect per Task 4.1 — gitter struck by amendment 10.

## Amendments round 2 — 2026-08-29 evening (user-ruled; verified against `main` @ `ff7460e`; these override any matching text above and below)

9. **The two-scope store layout is LANDED** (`main` @ `5b87cf3`): `templates/` = `global/` (machine-global originals) + `project/` (per-install) + `themes/` at root; **the directory IS the scope declaration**. Every store-relative path in this spec re-roots (`commands/git.md` → `global/commands/git.md`; `commands/dev.md` → `project/commands/dev.md`; `agents/per-project/*` → `project/agents/per-project/*`; `ptm.md` → `project/commands/pfm.md`, per amendment 6). `templates.toml` (§ 4.3) DROPS its `scope` field — scope derives from the path prefix, one source of truth; the `GLOBAL-TOKEN` gate keys off the `global/` prefix. `templates/adapters/<engine>/` is a new tree beside `global/` and `project/`.
10. **gitter is PROJECT scope** (user-ruled, landed @ `ff7460e`): every install customizes its own git specialist through its adapter/overrides; the template lives at `project/agents/gitter.md`. The global agent bench is exactly: architect, reviewer, rr, scheduler, tracer. Amendment 8's "detokenized gitter" and every Task 4.1 mention of a global gitter are struck.
11. **M4's registration half is SHIPPED** (`5b87cf3`): `pfm install` already symlinks global originals via `codexgen/globallink.go` — a classifier (missing/correct/copy/wrong-target/conflict + `UNREADABLE` as error never absence) whose conflict rule IS Task 4.1.d's (`CONFLICT {path}: not ours` — report-and-skip, never overwrite); global commands, skills, and committed `.toml` twins wire the same way, and missing twins self-heal at install. M4's remaining scope — the § 5.4.b demotion rule, Task 4.1.c's `GLOBAL-DANGLING-AGENT` check, `global-build.json` link-target lock, machine-scope overrides — is built ON `globallink.go`, never as a parallel mechanism. Known gap carried in: no uninstall-side symlink removal beyond the exact-name orphan sweep.
12. **`h/**` exists and is global** (`global/commands/h/gh.md`, curated original, landed): § 1.6's "nothing `/h:*` to globalize" and § 8's "/h:* does not exist" rows are obsolete — `h/` is already registered global and the compiler inherits it as a curated global template.
13. **Execution mode: NO train** (user-ruled): the main session executes the milestones directly, dispatching sonnet dev/qa agents (opus where § Model Selection demands frontier judgment) with five-field briefs; no `train.md`/`STATE.md`/CCC ceremony. Unchanged law: Go work builds and tests inside the fence (`dev.sh iso`, fence line quoted in every report) in the `.worktrees/blueprint-compiler/` worktree gitter creates; gitter alone commits, at each § 7 checkpoint; the § 7 user reviews remain binding — especially checkpoint 3 BEFORE Task 3.4's commit. § 0.2's prohibitions bind every dispatched agent verbatim.
14. **The first registered adopter is already half-migrated** (2026-08-29): it consumes the machine globals via symlinks today; its `/pcm` family is already `/pfm`; its surviving project-layer files — `agents/gitter.md`, `wave/{live,orchestrator,walker}.md`, `quality/doc.md`, plus its in-flight `docs/commands/`→`docs/references/` migration — are the pre-identified seed of its `.professor/overrides/` at its own Task-3.2-equivalent reconciliation. Its wave/walker "Recipes" table migrates into its project-scope `wave/walker-invariants.md` data card at that pass.
15. **`refresh-scope.sh` interim fix** (landed @ `5b87cf3`): `resolve_path` now classifies an unresolvable `{project:role}` source as `MISSING-SOURCE` (still blocking, exit 3) instead of aborting the scan at first hit — the gate stays honest until Task 3.1.c deletes the whole refresh mechanism.

---

This document is written to be executed without judgment calls. Where it says "exactly", copy it. Where a decision was possible, it has been made. Where a decision is the user's (§ 7 checkpoints), the table is pre-filled and the user approves or amends it at the checkpoint — you never fill it yourself. If you meet a situation this document does not cover, **stop and report it** (§ 9).

---

## 0. Before you touch anything

### 0.1 Where you work

- Your checkout is the worktree `.worktrees/blueprint-compiler/` on branch `train/blueprint-compiler`. **A gitter session creates it before you start.** If it does not exist, stop and report `PREREQ MISSING: worktree .worktrees/blueprint-compiler`.
- The branch must contain the engine contract. Check: `ls pfm/internal/engine/` prints at least one `.go` file and `grep -rn 'func All()' pfm/internal/engine/` prints a line. If either prints nothing, stop and report `PREREQ MISSING: engine-contract`.
- Every path in this document is relative to that worktree root.

### 0.2 What you may never do

- **Never run `go test`, `go build`, or `go vet` on the host.** Every build and test runs inside the fence (§ 0.3). The jail guard makes an unfenced `go test` fail with `refusing to resolve the operator's real home directory inside a test` — that line means you ran outside the fence.
- **Never run a state-changing git command** (`commit`, `push`, `tag`, `merge`, `rebase`, `stash`, `checkout`, `reset`, `worktree`). Only the gitter agent writes git; it commits at the checkpoints in § 7.
- **Milestones 1, 2, 4: never edit `.claude/**`, any `CLAUDE.md`, any `AGENTS.md`, `.codex/**`, `.opencode/**`.** Milestone 3 regenerates those trees *through the compiler* — still never by hand (§ 5.3 says exactly how).
- Never write a machine-absolute path (`/home/…`, `/Users/…`) into any tracked file. `scripts/leak-check.sh` is the backstop, not the plan.
- Never change `pfm/internal/store/store.go`'s `SchemaVersion`; this wave touches no database.
- Never push, tag, or release. Not at the end either.

### 0.3 How you build and test

```
cd .worktrees/blueprint-compiler
./.claude/scripts/dev.sh iso test pfm      # full suite inside the container
./.claude/scripts/dev.sh iso verify pfm    # vet + gofmt + build inside the container
./.claude/scripts/dev.sh iso shell         # interactive shell INSIDE the fence for fast iteration
```

A green run ends with `all steps passed.` and prints one line `fence: container=<id> HOME=/root work=/worktree`. **Quote that line in every report.** A report without it is a report of an unfenced run. Golden files (`pfm/testdata/golden/*.ansi`) regenerate on the host with `PFM_UPDATE_GOLDENS=1` (the container mounts the worktree read-only), then re-verify inside the fence.

The host mirror (`go -C pfm build -o ~/.local/bin/pfm ./cmd/pfm` + `pfm install --yes` + `pfm doctor`) is built by the orchestrating session after the gitter merge — never by you.

---

## 1. Why (verified 2026-08-23 on `main` @ `c75d4cc`; line numbers drift — grep the quoted text)

1. **The override engine exists and nobody uses it.** `pfm/internal/codexgen/override.go` implements `replace-section` (by `headingPath`), `replace-exact` / `delete` / `insert-after` (by literal anchor), each pinned with a `sourceHash` and reporting `applied` / `anchor-missing` / `stale-pin` (`OverrideStatus`, `ApplyOverrides`). It reads a legacy project override directory through `config.go`. No such directory exists in this repo; no `.md` file documents it. It is the seed of this design — built for one narrow job (Codex) when it is the missing layer for the whole framework.
2. **Three compilers for one job.** `pfm codex build` is the single writer of the Codex mirror (`.claude/scripts/codex-sync.sh:6,21,47`); `.claude/scripts/build-codex.mjs` (475 lines) still exists and root `CLAUDE.md` § Three-runtime team still tells you to run it — a stale pointer; `.claude/scripts/build-opencode.mjs` (452 lines) is a third, hand-maintained twin. Three implementations of alias swap, persona strip, `/x`→`$x`, frontmatter parsing.
3. **Install copies, so update must merge.** `templates/**` templates carry `{TOKENS}`; `docs/SETUP.md` Phase 2 has an LLM fill them and copy files into `.claude/`; `/ptm:update` (`templates/commands/ptm/update.md`) is an LLM three-way hash merge per file. Every local edit is a future merge conflict; every update is a language-model judgment over prose.
4. **This repo keeps two copies of every prompt equalized by an LLM.** Live `.claude/**` is the source; `templates/**` is re-derived by `scripts/refresh-scope.sh` + `templates/refresh-map.json` (631 lines of hashes) — "UNCHANGED hashes are a mechanical untouched-proof — skipped" — and the rest is LLM re-derivation. The 24 files present in both trees all differ (`diff templates/commands/git.md .claude/commands/git.md` = 21 lines; `wave/live.md` = 77). `.professor/drift.md` carries 16 `KEEP-LOCAL` bullets explaining, in prose, why.
5. **The symlink pattern that works is pfm's.** `pfm install` stages assets under `~/.local/share/pfm/install/` and symlinks `~/.claude/commands/chat/*.md` → store (`installer.go` `ensureLink`, `commandTarget`). Update = replace the store; registration follows. Zero merge — and zero customization.
6. **Placeholder surface, measured.** Tokens per template (`grep -oE '\{[A-Z][A-Z0-9_]+\}' | sort -u | wc -l`): `commands/git.md`=0, `wave/builder.md`=0, `wave/orchestrator.md`=0, `wave/ccc.md`=1, `wave/live.md`=1, `p/tokens/SKILL.md`=0, `p/slow-burn.md`=0, `sleep.md`=0, `goal-manager.md`=0, `animate.md`=0, `agents/scheduler.md`=0, `agents/architect.md`=0, `agents/tracer.md`=1, `agents/gitter.md`=2, `ptm.md`=2, `wave/refine.md`=5, `quality/prompt.md`=6, `wave/walker.md`=9, `dev.md`=19, `scripts/dev.sh`=36, `output-styles/professor.full.md`=23. No `commands/h/**` exists in `templates/` or `.claude/` (it is an `ignore_sources` entry in `refresh-map.json`, i.e. a private-source command never shipped) — there is nothing `/h:*` to globalize; § 5.4 records this.
7. **Harness facts** (Claude Code docs, `code.claude.com/docs/en/slash-commands.md`, read 2026-08-23): same-named skills resolve **enterprise > personal > project** — a project copy does NOT shadow a `~/.claude/` copy; for same-named *commands* the precedence is NOT DOCUMENTED. Symlinked skill dirs are followed and deduplicated; symlinks under `~/.claude/commands/` are NOT DOCUMENTED but demonstrably work (the `/chat:*` family runs through them daily). `${CLAUDE_PROJECT_DIR}` is substituted in user-scope commands. Whether a user-scope command may `subagent_type` a project-scope agent is NOT DOCUMENTED. Consequences: § 5.4 scope-demotion rule; global commands ship with global agents.
8. **`pfm update` today** (`pfm/cmd/pfm/update_command.go`: `runUpdate` → `updateRepository` → `buildUpdateCandidate` → `applyUpdateInstall` → `runUpdateDoctor`) updates the *source clone* recorded by `installer.ReadSourceRepoMarker` to the highest semver tag, rebuilds the binary transactionally with rollback, then runs `install --yes` and `doctor`. It never touches a project's `.claude/`. `pfm init` (`init_command.go`: `initScaffold`, `discoverSourceRepo`, `isSourceRepo`) copies a scaffold and prints "open Claude here and follow docs/SETUP.md".

---

## 2. The user's rulings (verbatim where quoted — these are the law of this spec)

1. "**Blueprint MUST be the only source of truth.** Which has to get compiled to multiple outputs with override engine as middleware."
2. "I want 2 kinds of middleware: 1. our default professor override for different kinds of engine. 2: customized middleware for whoever wants to change something, which this one goes into some **gitignored place**, so with new updates, they don't get lost."
3. "Usually `pfm update` will be called by a model, so if it **returns what it did, and asks for review**, people won't be having silent failure and their model/harness/agent tells them."
4. "The other place is just local projects that gets compiled to, just like this with 2 middlewares." — this repo is one more compiled project, not a special case.
5. "We need **comment on generated ones and our own blueprint**, to tell the model that is about to edit it, to do the work on customizable middleware."
6. Registration of globalized commands: **truly global** (`~/.claude/commands` and each engine's equivalent).
7. Customization UX: **hand-written override JSON** (no edit-and-capture command).
8. Delivery: all specs in one file, handed to a builder as a whole.

---

## 3. Architecture

### 3.1 The pipeline (one function, every output)

```
template (store/<path>, tokens intact)
  → user overrides     .professor/overrides/all/*.json            (project, layer 2a)
  → engine adapter     store/adapters/<engine>/*.json             (shipped, layer 1)
  → user overrides     .professor/overrides/<engine>/*.json       (project, layer 2b)
  → substitution       {TOKEN} ← manifest answers; {{#each}}/{{#if}} directives
  → engine transform   internal/engine registry: alias swap, /x→$x, flatten, TOML wrap, marker
  → write              atomic; hash recorded in .professor/build.json
```

Overrides operate on **template text with tokens intact**, so a `sourceHash` pin is stable across projects and changes only when upstream changes. Layer order is fixed: a user's engine-neutral customization is applied before the shipped adapter (an adapter that must replace a section for its engine wins over a neutral tweak), and a user's engine-specific customization is applied last (it wins over everything). Every layer pins against the text *it* was authored on; a pin that no longer matches is a named status, never a silent skip.

### 3.2 Vocabulary

| Term | Meaning | Location |
| --- | --- | --- |
| **store** | an immutable checkout of `templates/**` at one release tag | `$PFM_DATA/professor/store/<tag>/` (`$PFM_DATA` = `~/.local/share/pfm`); `current` symlink → the active tag. **Self-hosted** (`manifest.installed_from.mode == "self-hosted"`): store = `<root>/templates/`, version = `VERSION` + `git rev-parse HEAD` |
| **adapter** | shipped middleware for one engine: `engine.json` + override files | `templates/adapters/<engine>/` |
| **overrides** | project middleware (hand-written JSON) | `.professor/overrides/{all,claude,codex,opencode}/*.json` — gitignored by default (§ 3.6); **machine** overrides for global outputs: `$XDG_CONFIG_HOME/pfm/professor/overrides/{all,<engine>}/` |
| **manifest** | the interview answers + selection | `.professor/manifest.json` (exists; gains `scope` and `overrides` keys, § 4.5) |
| **lock** | what the compiler wrote, per output | `.professor/build.json` (project) · `$PFM_DATA/professor/global-build.json` (machine) |
| **registry** | every project root this machine has built | `$PFM_DATA/professor/projects.json` (host-local, never tracked, absolute paths allowed here and only here) |
| **template metadata** | per-template scope/tier | `templates/templates.json` (§ 4.3) |
| **placeholder registry** | token → manifest path | `templates/placeholders.json` (§ 4.2) — the machine form of `docs/PLACEHOLDERS.md`, which remains the human form and must agree |

### 3.3 Outputs per engine (project scope)

| engine | outputs |
| --- | --- |
| claude | `CLAUDE.md`, `<roster.dir>/CLAUDE.md`, `.claude/commands/**`, `.claude/agents/*.md`, `.claude/scripts/*`, `.claude/output-styles/*.md`, `.claude/settings.json`, `.claude/workflows/*.js` |
| codex | `AGENTS.md`, `<roster.dir>/AGENTS.md`, `.codex/agents/*.toml`, `.codex/skills/<flat>/SKILL.md`, `.codex/rules/*`, the managed `mcp_servers` fence in `.codex/config.toml` |
| opencode | `.opencode/agent/*.md`, `.opencode/command/<flat>.md`, `.opencode/skills/*` (dir symlinks), the three managed keys of `.opencode/opencode.jsonc` |

Machine scope (§ 5.4): `~/.claude/commands/**`, `~/.claude/agents/*.md` · `~/.codex/skills/<flat>/SKILL.md`, `~/.codex/agents/*.toml` · `~/.config/opencode/command/<flat>.md`, `~/.config/opencode/agent/*.md`.

Engine-specific *syntax* stays code, behind the engine registry from `engine-contract`: model-alias swap (`modelMap`), `/name`→`$name` and nested-name flattening, persona-adoption line strip, `CLAUDE.md`→`AGENTS.md` in-body rewrite, TOML wrapping (`toml.go`), `.mcp.json` fence (`mcp.go`), marker placement (§ 3.5). Engine-specific *content* (the Codex adapter section, the OpenCode adapter section, the agent preamble) is data in `adapters/<engine>/*.json`.

### 3.4 Statuses — the vocabulary every surface speaks

| status | meaning | what the compiler does |
| --- | --- | --- |
| `built` | output written (or unchanged) from a clean layer stack | write |
| `applied` | an override matched exactly once and its pin matched | (per override) |
| `stale-pin` | an override's anchor still matches but the pinned region changed upstream | **hold** the file (§ 3.7), exit `3` |
| `anchor-missing` | an override's heading/anchor matched 0 or >1 times | hold, exit `3` |
| `missing-answer` | a template needs a token the manifest lacks | hold, exit `3` |
| `hand-edited` | output on disk ≠ lock hash (someone edited a generated file) | **do not overwrite**; report; exit `3` |
| `scope-demoted` | a global template became project-scoped on this machine (§ 5.4) | write project copies, remove global copy, report |
| `orphan` | an output in the lock whose template no longer exists | delete, report |
| `held` | a file whose previous output was kept because of one of the above | (state in the lock) |
| `UNREADABLE` | store / manifest / override / output could not be read (not `ENOENT`) | abort with the path and the OS error; exit `1` |

An `ENOENT` on an optional input (no overrides dir, no adapter for an engine) is `none` — reported as a count of zero, never as an error. Any other OS error is `UNREADABLE`, never folded into "nothing there". This is root `CLAUDE.md`'s absence-vs-error law and it binds every probe in this wave.

### 3.5 Markers — the comment on generated files and on the blueprint (ruling 5)

Generated outputs carry, in the first parseable comment position of their file type, exactly this text (one line; `{src}` = store-relative template path; `{ver}` = store tag or `self-hosted@<short-sha>`):

```
Generated by pfm professor build from {src}@{ver} — DO NOT EDIT. Framework change → edit templates/{src} and rebuild. Project customization → write an override under .professor/overrides/ (pfm professor override --help).
```

Placement by file type: frontmatter files (commands, agents, skills, output-styles) → a YAML comment line `# …` as the first line *inside* the frontmatter (after the opening `---`; the harness ignores YAML comments, and a comment before the fence would break frontmatter parsing); `CLAUDE.md` / `AGENTS.md` / `.md` without frontmatter → `<!-- … -->` as line 1; `.sh`/`.zsh` → `# …` on line 2 (after the shebang); `.mjs`/`.js` → `// …` line 1; `.toml` → `# …` line 1; `.jsonc` → `// …` line 1; `.json` → no in-file marker (JSON has no comments) — the lock alone owns it, and `check` reports `hand-edited` from the hash.

Blueprint sources carry, in the same position, exactly:

```
professor: SOURCE TEMPLATE — edit here for a framework change (routes through /ptm); a project-only customization is an override under .professor/overrides/, never an edit to a generated copy.
```

The compiler replaces the source marker with the generated marker; a source missing its marker is a build error `SOURCE-MARKER-MISSING {src}` (the gate that keeps rule 5 true for every template, forever).

### 3.6 The gitignore rule (ruling 2)

`pfm professor build` ensures the project's `.gitignore` carries this exact stanza when `manifest.overrides.tracked` is `false` (the default an installer writes), and removes it when `true`:

```
# professor: project middleware + build lock — local by default; set .professor/manifest.json overrides.tracked=true to share with a team
.professor/overrides/
.professor/build.json
```

This repo sets `overrides.tracked: true` (§ 5.3): it is the upstream and must reproduce itself from a clone.

### 3.7 Hold semantics (ruling 3)

A file with any non-clean status is **not rewritten**: the previous output stays on disk byte-for-byte, the lock marks it `held` with the reason, the report names the file, the override (path), the reason, and the one action that clears it. A hold is never silent and never resolved by the compiler choosing a side. A build with ≥1 hold exits `3` (`REVIEW REQUIRED`); a build that could not read an input exits `1`; clean exits `0`. The report format is § 6 — a model reading it sees exactly what to ask its human.

---

## 4. Formats

### 4.1 Override file, version 2 (replaces codexgen's v1; v1 files are rejected with `OVERRIDE-V1 {path}: rewrite as version 2`)

```json
{
  "version": 2,
  "target": "commands/quality/prompt.md",
  "mode": "replace-section",
  "headingPath": ["Mandatory skill load"],
  "content": "## Mandatory skill load\n\n…full replacement section, heading included…\n",
  "sourceHash": "sha256:…",
  "note": "why this project differs (free text, shown in reports)"
}
```

- `target`: store-relative template path (`CLAUDE.md`, `per-project/CLAUDE.md`, `commands/git.md`, `agents/gitter.md`, `settings.json`, …). Engine is **directory placement**, never a field: `overrides/all/`, `overrides/claude/`, `overrides/codex/`, `overrides/opencode/`; adapters are implicitly their engine.
- `mode` ∈ `replace-section` (needs `headingPath`: the exact heading texts from the document root, `#` stripped, to the section replaced including its subsections) · `replace-exact` / `delete` / `insert-after` / `insert-before` (need `anchor`: a literal substring that must occur exactly once) · `json-merge` (target is JSON; `content` is an object deep-merged, arrays replaced; for `settings.json`, `opencode.jsonc`) · `add-file` (`target` does not exist upstream; `content` is the whole file; the generated marker is still stamped) · `drop-file` (the template produces no output for this project/engine; the lock records `dropped` so a later removal of the override restores it).
- `sourceHash`: `sha256:` over the exact region the mode replaces/deletes/anchors on, in the text of the layer below (§ 3.1). Required for every mode except `add-file`. `pfm professor override new --target T --mode M --heading "A/B"` (or `--anchor "…"`) prints a ready file with the current hash — scaffolding only; the JSON stays the artifact (ruling 7).
- Files apply in lexical order within a directory. Two overrides on the same region: the second reports `stale-pin` (its pin was authored on the unmodified region). That is correct and reported.

### 4.2 `templates/placeholders.json`

```json
{
  "version": 1,
  "tokens": {
    "PROJECT_NAME":    { "path": "answers.project_name", "required": true },
    "PROJECT_TAGLINE": { "path": "answers.tagline",      "required": true },
    "FRONTIER_MODEL":  { "path": "answers.models.frontier", "required": false, "default": "opus" },
    "…": "one entry per token in docs/PLACEHOLDERS.md — the builder transcribes the whole table; a token in a template but not here is BUILD ERROR `UNREGISTERED-TOKEN {token} in {src}`"
  },
  "roster": {
    "each": "answers.roster",
    "fields": { "PROJECT": "dir", "PROJECT_ROLE": "role", "PROJECT_STACK": "stack", "PROJECT_PKG_MGR": "pkg_mgr", "PROJECT_TEST_RUNNER": "test_runner", "PROJECT_PORT": "ports[0]" }
  }
}
```

Directives, line-oriented, valid in any file type because they sit on their own line and are removed: `{{#each roster}} … {{/each}}` (body repeated per entry; roster tokens resolve per entry), `{{#if answers.codex}} … {{/if}}` and `{{#unless …}}` (boolean or non-empty test on a manifest path). Nothing else — no expressions, no partials. A `{TOKEN}` outside an `each` block that is a roster token is `BUILD ERROR ROSTER-TOKEN-OUTSIDE-EACH`.

### 4.3 `templates/templates.json`

```json
{
  "version": 1,
  "templates": {
    "commands/git.md":      { "scope": "global",  "tier": "A" },
    "commands/dev.md":      { "scope": "project", "tier": "A" },
    "commands/wave/live.md":{ "scope": "global",  "tier": "A" },
    "agents/gitter.md":     { "scope": "global",  "tier": "A" },
    "agents/per-project/developer.md": { "scope": "project", "tier": "A", "per": "roster" },
    "commands/marketer.md": { "scope": "project", "tier": "B", "optin": "marketer" }
  }
}
```

Every template under `templates/{agents,commands,scripts,output-styles,skills,workflows,settings.json,settings-global.json,CLAUDE.md,per-project/CLAUDE.md,codex/**}` has an entry or the build fails `UNLISTED-TEMPLATE {src}`. `scope: global` is legal only for a template with zero tokens after § 5.4 detokenizing — `GLOBAL-TOKEN {src}: {TOKEN}` otherwise. `optin` names the `answers.tier_b_roles` entry that selects it; `per: roster` emits one output per roster entry (today's `per-project/*`).

### 4.4 `templates/adapters/<engine>/engine.json`

```json
{
  "version": 1,
  "engine": "codex",
  "modelMap": { "opus": "gpt-5.6-sol", "sonnet": "gpt-5.6-luna", "haiku": "gpt-5.6-luna" },
  "agentPreamble": "…the current .claude/codex-build.json agentPreamble, verbatim…",
  "neverRegister": ["gitter"],
  "excludeProjects": ["templates"]
}
```

Plus override files in the same directory — the current `rootAdapter` text becomes `adapters/codex/10-root-adapter.json` (`mode: insert-after`, anchored on the last line of root `CLAUDE.md`'s final section; the OpenCode half becomes `adapters/opencode/10-root-adapter.json`). The example the user gave — "the agent-selection part completely replaced for Codex" — is `adapters/codex/20-model-selection.json` with `mode: replace-section`, `headingPath: ["Model Selection"]`, and content that names the two Codex tiers instead of four Claude aliases. The builder writes that file with content derived from root `CLAUDE.md` § Model Selection as it stands in the store, keeping every rule and swapping only the tier roster.

### 4.5 Manifest additions (`.professor/manifest.json`)

```json
"scope":     { "project": ["commands/quality/prompt.md"] },
"overrides": { "tracked": false },
"engines":   ["claude", "codex"]
```

`scope.project` lists global-scoped templates this project pins to project scope (§ 5.4 demotion). `engines` replaces the boolean `answers.codex` (kept readable for one release: `codex: true` ⇒ `engines` gains `codex`; the build reports `MANIFEST-LEGACY answers.codex → engines` once).

### 4.6 Lock (`.professor/build.json`)

```json
{
  "version": 1,
  "store": { "mode": "tag", "version": "v0.55.0" },
  "builtAt": "2026-08-23T12:00:00Z",
  "outputs": {
    ".claude/commands/git.md": { "template": "commands/git.md", "engine": "claude", "sha256": "…", "status": "built", "layers": ["adapters/claude/…", "overrides/all/03-git.json"] },
    "AGENTS.md": { "template": "CLAUDE.md", "engine": "codex", "sha256": "…", "status": "held", "reason": "stale-pin overrides/codex/01-law.json" }
  }
}
```

---

## 5. Tasks

All tasks: Go 1.24, package layout per `pfm/internal/*` conventions, every `if err != nil` carries the path and operation, no swallowed errors, tests in the package (`_test.go`) with `t.TempDir()` stores — never the host's `$HOME` (the jail guard enforces this). Every new check names what it reports when it is itself broken (the `UNREADABLE` row of § 3.4 is that answer for every probe that reads a file).

### Milestone 1 — the compiler (`pfm professor build|check|override|status`)

#### Task 1.1 — package `pfm/internal/professor`: store, manifest, placeholders, directives

**Why:** the substitution step is the only step today done by an LLM; it must be a pure function.

**Routing:** pfm · **Build agents:** dev (sonnet) implements, qa (sonnet) tests.

**Key behaviors:**
- a. `store.Open(root, manifest)` → `Store{Path, Version, Mode}`; tag mode reads `$PFM_DATA/professor/store/current`; self-hosted reads `<root>/templates` + `VERSION` + `git rev-parse --short HEAD` (a missing `.git` is `self-hosted@unknown`, reported, never fatal).
- b. `manifest.Load(root)` parses `.professor/manifest.json`; unknown top-level keys are preserved byte-for-byte on write (the interview answers are the user's); `answers.codex` legacy mapping per § 4.5.
- c. `placeholders.Load(store)` + `Substitute(text, manifest, registry) (string, []Missing, error)`; `Missing` carries token + template path; directives per § 4.2; the substituted text is NOT written when `len(Missing) > 0` (hold).
- d. `templates.Load(store)` per § 4.3; `Select(manifest)` returns the template set for this project (tier A always; tier B by `optin`; `per: roster` expanded).

**File plan:** `pfm/internal/professor/store.go`, `manifest.go`, `placeholders.go`, `directives.go`, `templates.go`, each with `_test.go`.

**Tests (RED first, watched failing):** substitution of every token class incl. an `each` block with a 1-entry and a 3-entry roster; `UNREGISTERED-TOKEN`; `ROSTER-TOKEN-OUTSIDE-EACH`; `missing-answer` holds; a store dir with mode `000` → `UNREADABLE` with the OS error text, never "no store".

**Boundaries & anchors:** no engine names here (engine-contract law). Reuse `pfm/internal/paths` for `$PFM_DATA`/`$XDG_CONFIG_HOME` resolution — grep `paths.Resolve` before adding a path helper.

---

#### Task 1.2 — override engine v2 (move + extend `codexgen/override.go`)

**Why:** § 1.1 — the engine exists; it needs the v2 schema (§ 4.1), directory-placement engines, `json-merge`, `add-file`, `drop-file`, `insert-before`, and hold semantics instead of an aborting error.

**Routing:** pfm · **Build agents:** dev, qa.

**Key behaviors:**
- a. `overrides.Load(dir) ([]Override, []Problem)` — a malformed JSON file is a `Problem` naming path + parse error; the rest still load. v1 files → `OVERRIDE-V1`.
- b. `Apply(text, []Override) (string, []Status)` — never returns an error for a content mismatch; `stale-pin` / `anchor-missing` become statuses and the caller holds the file. Only an I/O failure is an error.
- c. Region hashing as § 4.1; `replace-section` region = heading line through the line before the next heading of equal-or-higher level (or EOF).
- d. `json-merge`: parse target as JSON (jsonc: strip `//` and `/* */` comments first, preserving the generated comment header on write); deep-merge objects, replace arrays/scalars; a non-JSON target → `anchor-missing` with reason `not-json`.

**File plan:** `pfm/internal/professor/override.go` (+ `_test.go`); `pfm/internal/codexgen/override.go` deleted in Task 1.5.

**Tests (RED first):** one test per mode; ordering (lexical) test; two overrides on one region → second is `stale-pin`; `add-file` stamps the marker; `drop-file` records `dropped`; JSON merge preserves unknown keys; jsonc comments survive.

**Boundaries & anchors:** keep codexgen's byte-level (non-reserialized) application for Markdown — `override.go` already does this ("kept as bytes … never introduces unrelated formatting churn"); carry that comment forward.

---

#### Task 1.3 — emitters behind the engine registry + markers + lock

**Why:** three compilers → one; the engine-specific syntax goes where engine-contract says engine behaviour lives.

**Routing:** pfm · **Build agents:** dev, qa.

**Key behaviors:**
- a. Each engine descriptor (from `engine.All()`) exposes an emit contract: `Outputs(template, scope) []OutputPath`, `Transform(text, TransformContext) (string, error)`, `Marker(fileType) (prefix, suffix)`. The claude emitter is near-identity (marker + substitution only). The codex emitter absorbs `codexgen/transform.go`, `toml.go`, `mcp.go`, `reconcile.go` (`ManagedFence` for `config.toml`), `globalagents.go`. The opencode emitter absorbs the transforms of `.claude/scripts/build-opencode.mjs` (flatten, persona strip, skill dir symlinks, the three managed `opencode.jsonc` keys with the unmarked-file CONFLICT rule — read that script's header comment, lines 1–40, and reproduce each rule with a test).
- b. `build.Run(Options{Root, Home, Engines, Scope, Mode})` composes § 3.1 per template per engine, writes atomically (temp + rename), records § 4.6; `Mode: check` writes nothing and reports what build would do.
- c. Hand-edit detection: before writing, compare on-disk hash with the lock's; mismatch → `hand-edited`, hold.
- d. Orphans: lock entries whose template vanished → delete the output, report `orphan`.
- e. `.gitignore` stanza per § 3.6.
- f. Exit codes per § 3.7.

**File plan:** `pfm/internal/professor/build.go`, `lock.go`, `report.go`; per-engine emit files wherever `engine-contract` placed per-engine behaviour (read that spec's file plan — do not invent a second home); `pfm/cmd/pfm/professor_command.go` with subcommands `build [--engine E]... [--scope project|global|all] [--root DIR] [--json]`, `check` (same flags), `override new …` (§ 4.1), `status` (prints the lock as the § 6 table).

**Tests (RED first):** golden build of a fixture store with 1 template × 3 engines × project scope, byte-compared; marker placement per file type (frontmatter comment stays inside the fence — assert the first line is still `---`); hand-edited hold leaves bytes untouched; orphan removal; `check` is side-effect-free (hash the tree before/after); exit codes 0/1/3.

**Boundaries & anchors:** the TOML escaping in `codexgen/toml.go` and the frontmatter parser in `transform.go` (`parseFrontmatter`, block scalars) move verbatim — they were written to reject malformed fences, keep that.

---

#### Task 1.4 — `pfm professor` wired into `doctor`

**Why:** a stale or held build must show where the operator already looks.

**Key behaviors:** `pfm doctor` gains a `professor` section per registered project (registry § 3.2): `built N · held N (reasons) · hand-edited N · store vX`. An unreadable lock is `UNREADABLE`, never "no build". Registered projects whose root no longer exists are reported `MISSING-ROOT` and left in the registry (the user removes them with `pfm professor forget DIR`).

**File plan:** `pfm/cmd/pfm/doctor.go` (extend), `pfm/internal/professor/registry.go` (+tests).

---

#### Task 1.5 — retire `codexgen`; `pfm codex build|check` become aliases until M3

**Key behaviors:** `pfm codex build` ≡ `pfm professor build --engine codex --scope project`, `pfm codex check` ≡ `… check`, printing `deprecated: use pfm professor …` on stderr. `pfm codex agents` ≡ `pfm professor build --engine codex --scope global`. `pfm/internal/codexgen/` is deleted once its tests are ported (every test in `codexgen_test.go` and `globalagents_test.go` has a successor in `internal/professor` or the codex emitter — list the mapping in the report). `.claude/codex-build.json` is read for ONE release as a legacy source of `modelMap`/`agentPreamble` when `adapters/codex/engine.json` is absent, with `MANIFEST-LEGACY` reported; M3 deletes it.

**[MILESTONE]** — gitter commit `feat(pfm): professor compiler — one pipeline, three engines, override middleware`.

---

### Milestone 2 — update as a reviewed transaction

#### Task 2.1 — the store: fetch, pin, switch

**Key behaviors:**
- a. `pfm professor store fetch [--to vX.Y.Z]` — from the source clone recorded by `installer.ReadSourceRepoMarker` (reuse `update_command.go`'s `selectHighestSemver`, `updateGitRun`): `git archive <tag> templates` → `$PFM_DATA/professor/store/<tag>/` (atomic: extract to `<tag>.partial`, rename); `current` symlink switched only after extraction verified (`templates.json` parses). A tag without `templates/templates.json` is `STORE-PRE-COMPILER {tag}` — refused; the user is told the first compiler-aware release.
- b. `pfm professor store ls` — tags on disk, `current` marked.

**File plan:** `pfm/internal/professor/storefetch.go` (+tests with a throwaway git repo, as `update_command_test.go` already does — reuse its helpers).

#### Task 2.2 — `pfm update` re-materializes every registered project

**Key behaviors:** after `runUpdateDoctor` succeeds: `store fetch` → for each registry entry: `professor build --scope project` → then one `build --scope global`. Each project's § 6 report is printed; the final line aggregates: `update: vA → vB · projects N · clean N · review-required N · failed N`. Exit: `3` if any project holds, `1` if any failed, else `0`. A project whose root is missing is `MISSING-ROOT`, counted under failed, not skipped silently. `--dry-run` runs `check` everywhere and writes nothing. The build of the pfm binary itself (existing behaviour) is unchanged and still precedes all of this.

**File plan:** `pfm/cmd/pfm/update_command.go` (extend after `runUpdateDoctor`), tests extend `update_command_test.go`.

#### Task 2.3 — `pfm init` writes the manifest skeleton; `/ptm:update` shrinks to the interview delta

**Key behaviors:**
- a. `pfm init [dir]` creates `.professor/manifest.json` with every `required: true` token's answer path present and `null`, `engines: ["claude"]`, `overrides.tracked: false`, `scope.project: []`, `installed_from` = store mode+version; prints a handoff to the install interview. It no longer copies a file scaffold (`initScaffold`, `copyInitTree`, `copyInitFile` deleted; `discoverSourceRepo`/`isSourceRepo` stay for the source-marker fallback).
- b. `templates/commands/ptm/update.md` is rewritten: it runs `pfm update --json`, reads the report, and does exactly two LLM things — asks the interview question for each `missing-answer` token (writing the answer into the manifest) and presents each `held` file with its one clearing action; then re-runs `pfm professor build`. The three-way merge prose is deleted entirely. `templates/commands/ptm/install.md` (new) is the interview: SETUP.md Phase 1 questions → manifest, then `pfm professor build`.
- c. `docs/SETUP.md` Phase 2 (LLM customization), Phase 3's copy steps, and `docs/BLUEPRINT.md` § "Staying current" are rewritten to describe § 3 (the builder rewrites; the user reviews at the checkpoint). `INSTALL.md` paths 1 and 2 end in `pfm init` plus the install interview.

**Publication surface:** `templates/**`, `INSTALL.md`, `docs/*.md` — invented example values only; run `scripts/leak-check.sh` before the checkpoint.

**[MILESTONE]** — gitter commit `feat(pfm): pfm update re-materializes every project and asks for review`.

---

### Milestone 3 — invert this repo (ruling 1 and 4)

This is the one milestone that edits guarded trees, and it does so only by running the compiler. Sequence is mandatory.

#### Task 3.1 — blueprint becomes compiler-ready

**Key behaviors:**
- a. Stamp the source marker (§ 3.5) on every template; write `templates/placeholders.json` (from `docs/PLACEHOLDERS.md`, complete), `templates/templates.json` (every template; scopes per § 5.4 table), `templates/adapters/{claude,codex,opencode}/engine.json` + override files (§ 4.4; content lifted verbatim from `.claude/codex-build.json` and `build-opencode.mjs`'s permission block).
- b. Convert representative-pattern templates into `{{#each roster}}` blocks: the set is every template `docs/PLACEHOLDERS.md` § Project roster names as roster-tokenized (grep the roster tokens of § 4.2 across `templates/`); each conversion is byte-verified by building against this repo's manifest and diffing against the live file (Task 3.2 makes that diff the acceptance test).
- c. `templates/refresh-map.json`, `scripts/refresh-scope.sh`, `templates/commands/ptm/references/refresh.md`, `docs/commands/ptm/references/refresh.md`, `templates/scripts/build-codex.mjs` — **deleted**, with every reference scrubbed (`README.md`, `docs/BLUEPRINT.md`, `docs/SETUP.md`, `templates/commands/ptm.md`, `templates/commands/ptm/release.md` step that regens the map, `.claude/commands/ptm/release.md` via its template).

#### Task 3.2 — reconcile live vs blueprint (the one-time honest pass)

**Why:** 24 files exist in both trees and differ; 8 live-only files exist; the blueprint carries 60+ files this repo does not install. After this task, `pfm professor build --scope all` in this repo reproduces `.claude/**`, `CLAUDE.md`, `pfm/CLAUDE.md` (and every roster `CLAUDE.md`), `AGENTS.md`s, `.codex/**`, `.opencode/**` **byte-identically** to what is committed — or the difference is a deliberate, listed change.

**Method (exactly):** for each file in both trees, `diff` live against the compiled output; classify each hunk as (i) a project VALUE → it is a token, fix `placeholders.json`/manifest; (ii) a framework IMPROVEMENT present only live → port it into the blueprint template (the blueprint is the source; live was ahead); (iii) a this-repo-only divergence → write an override under `.professor/overrides/` with a `note` quoting the matching `drift.md` KEEP-LOCAL bullet. Pre-filled dispositions from `.professor/drift.md` (the user amends at the checkpoint):

| live artifact | disposition |
| --- | --- |
| `agents/dev.md`, `agents/qa.md` (global dev/qa, no per-project pair) | (ii) SHIP as `templates/agents/dev.md`, `qa.md`; `templates.json` marks `per-project/developer.md`/`qa.md` as the `per: roster` alternative selected by a new manifest answer `agents.layout: global|per-project` (this repo: `global`) |
| `wave/ccc.md`, `wave/live.md` rewired to the fence cast | (ii) SHIP the fence pipeline as the blueprint's pipeline **if** the adopter pipeline (`wave/orchestrator.md` + `wave/builder.md`, worktree) is kept as the alternative under a manifest answer `pipeline: fence|worktree`; otherwise (iii) override. **Pre-filled: ship + answer.** |
| `gitter.md` trimmed (no SETUP/MERGE) | (iii) override `overrides/all/gitter-phases.json` — **Pre-filled: override**, because the trimmed form is this repo's fence choice |
| "no `/ptm:update` here" | selection: `scope.drop: ["commands/ptm/update.md"]` via a `drop-file` override |
| root `CLAUDE.md` lean rewrite (118 lines) | (ii) SHIP — the lean form is the better template; the blueprint's `CLAUDE.md` adopts it with tokens |
| `explore-deny.sh`, `codex-build.json` | `explore-deny.sh` → (ii) SHIP under `templates/scripts/`; `codex-build.json` → deleted (Task 3.1 adapters replace it) |
| `output-styles/professor.md`, `dr-house.md` (live) vs `.compact.md`/`.full.md` (blueprint) | the live files ARE the compiled `depth: compact` outputs — `templates.json` selects `.compact`/`.full` by `answers.persona.depth`, output name drops the suffix |
| `walker-invariants.md` § Engine Config repo-relative path; `args.project` without gate keys | (i) tokens: `{WALKER_SCRIPT_PATH}`, `{WALKER_PROJECT_PROFILE}` — this repo's manifest carries the values |
| `.claude/skills/deep-rr/**` (gitignored, source-fetched) | untouched — `skills/sources.json` law stands |

`drift.md` is then deleted; its history section moves to `.professor/build.json`'s `store` record and the KEEP-LOCAL bullets live on as override `note`s. `release.md` and `retro.md` are untouched.

#### Task 3.3 — hooks and guards follow the inversion

**Key behaviors:**
- a. `templates/settings.json` Stop hook: `codex-sync.sh` → runs `pfm professor build --scope project` when the turn edited `templates/**` or `.professor/overrides/**` (the mark/sync split stays: `codex-sync.sh mark` on PostToolUse, `sync` on Stop), then `pfm professor check`; a non-zero `check` prints its § 6 report into the session (the hook's stdout), never swallowed. Bash-driven writes remain outside hook coverage; `check` in the ptm `structure` audit is the backstop (keep the comment that says so).
- b. `ptm-guard.sh`: guarded set becomes `templates/**` + `.professor/overrides/**` (sources) AND the generated trees (`.claude/**`, every `CLAUDE.md`/`AGENTS.md`, `.codex/**`, `.opencode/**`) with a *different* deny text for generated files: `GENERATED by pfm professor build from templates/{src} — edit the template (framework) or write an override under .professor/overrides/ (project), then rebuild. Hand edits are detected and held.` The `/ptm` + `quality/prompt.md` unlock continues to govern the sources.
- c. Root `CLAUDE.md` template: § Three-runtime team rewritten around the compiler (one paragraph; the `build-codex.mjs`/`build-opencode.mjs` commands and the "AGENTS.md is compiled from this file" sentence are replaced by `pfm professor build`/`check`); § Repo structure's `.claude/` line becomes "generated from `templates/` — source of truth is the blueprint"; `scripts/refresh-scope.sh` mention removed. ≤ 200 lines.
- d. `.claude/scripts/build-opencode.mjs` and `.claude/scripts/build-codex.mjs` (if present) are gone because the blueprint no longer ships them; `pfm codex build|check|agents` aliases from Task 1.5 are removed; `settings.json` permission `Bash(pfm codex:*)` → `Bash(pfm professor:*)`.

#### Task 3.4 — flip the switch

1. In the worktree: `pfm professor build --scope project --root .` (self-hosted store = the worktree's `templates/`). Exit must be `0` — a hold here is a reconciliation miss: fix the template/override, never the output.
2. `git status --porcelain` lists exactly the generated trees plus the Task 3.1–3.3 edits. `git diff --stat -- .claude CLAUDE.md AGENTS.md .codex .opencode` is empty except for the marker lines and the deliberate changes listed in the Task 3.2 report.
3. `scripts/leak-check.sh` clean; `dev.sh iso all pfm` green with fence proof; `node` is no longer needed by any gate (`dev.sh status` says so).

**[MILESTONE]** — the user reviews the Task 3.2 table and the diff stat BEFORE gitter commits `feat(blueprint): blueprint is the only source — this repo compiles from it`.

---

### Milestone 4 — truly global commands and agents (ruling 6)

#### Task 4.1 — scope rules, detokenizing, demotion

**Key behaviors:**
- a. Initial `scope: global` set (from § 1.6, zero tokens today): `commands/git.md`, `wave/builder.md`, `wave/orchestrator.md`, `p/tokens/SKILL.md`, `p/slow-burn.md`, `sleep.md`, `goal-manager.md`, `animate.md`, `agents/scheduler.md`, `agents/architect.md`. **Detokenize to reach global:** `wave/ccc.md` (1), `wave/live.md` (1), `agents/tracer.md` (1), `agents/gitter.md` (2), `ptm.md` (2), `ptm/context-meter.md` (1), `p/rnd.md` (1), `p/360.md` (2) — for each token, the rule: a value a *command* needs at runtime is read from `${CLAUDE_PROJECT_DIR}/.professor/manifest.json` by the command's own prose (`pfm professor answer PROJECT_NAME` prints one answer — add it), and `{MODEL_TIER}` is an adapter concern (the claude adapter substitutes it from `engine.json`). `quality/prompt.md` (6 domain tokens), `wave/refine.md` (5), `wave/walker.md` (9) stay **project** scope in this wave — listed as the next detokenizing candidates in the report, not done.
- b. **Demotion rule** (§ 1.7): at global build, for each global template, if ANY registered project's `manifest.scope.project` names it, the global output is removed (lock `scope-demoted`, reported with the demanding project's path) and every registered project gets a project-scope output on its next build. A project override under `overrides/*/` targeting a global template, without a `scope.project` entry, is `SCOPE-CONFLICT {override}: target is global on this machine; add "{target}" to manifest.scope.project or move the override to $XDG_CONFIG_HOME/pfm/professor/overrides/` — held, exit 3. Machine overrides apply to global outputs; project overrides never do.
- c. Global agents travel with global commands: `gitter`, `scheduler`, `tracer`, `rr`(already), `architect` → `~/.claude/agents/`, `~/.codex/agents/*.toml` (existing `globalagents` path — a pfm-owned generated directory linked from there, never a file tracked in the blueprint clone), `~/.config/opencode/agent/`. A global command's `subagent_type` must resolve to a global agent — `pfm professor check` greps every `subagent_type:` in a global command against the global agent set and reports `GLOBAL-DANGLING-AGENT {cmd} → {agent}` (§ 1.7: cross-scope dispatch is undocumented; we do not rely on it).
- d. `~/.claude/commands/` ownership: the compiler owns exactly the paths in `global-build.json`; pfm `install`'s `chat/*` + `reload.md` symlinks are a different owner and are never touched (`commandTarget` stays as is). A foreign file at a path the compiler wants is `CONFLICT {path}: not ours` — held, never overwritten.

#### Task 4.2 — this repo goes global

`manifest.scope.project: []`; build `--scope all`; `.claude/commands/` retains only project-scoped templates (`dev.md`, `quality/*`, `wave/refine.md`, `wave/walker*.md`, `ptm/release.md`, `p/tokens/README.md`); the rest now resolve from `~/.claude/commands/`. `pfm doctor` shows both scopes. `/git`, `/wave:ccc`, `/wave:live`, `/ptm` invoked from this repo read identically (byte-diff the global output against the pre-M4 project output: only the marker's `scope` word differs).

**[MILESTONE]** — gitter commit `feat(professor): global scope — one command source per machine, demotion instead of shadowing`.

---

## 6. The report `pfm update` / `pfm professor build|check` print (ruling 3)

Human form (default), one block per project, then the machine summary; `--json` emits the same as one object.

```
professor: /path/to/project  store v0.54.0 → v0.55.0  engines claude,codex
  built        41   (38 unchanged, 3 changed: .claude/commands/git.md, AGENTS.md, .codex/agents/dev.toml)
  held          2
    .claude/commands/quality/prompt.md   stale-pin     overrides/all/02-prompt-audit.json → § "Mandatory skill load" changed upstream
                                         action: compare the section in the store (pfm professor override diff overrides/all/02-prompt-audit.json), then either re-pin (pfm professor override repin …) or delete the override
    CLAUDE.md                            missing-answer {DOMAIN_RISK_EXAMPLE}
                                         action: answer it — /ptm:update asks the question, or edit .professor/manifest.json answers.domain_risk_example
  hand-edited   1   .claude/agents/qa.md — not overwritten; action: move the edit into an override (pfm professor override new --target agents/qa.md …) or discard it (git checkout / delete the file) and rebuild
  orphans       0
  REVIEW REQUIRED — 3 items above need a human decision; nothing was overwritten.   exit 3
```

Rules: every held line carries exactly one `action:`; counts are printed even when zero; a probe that could not run prints `UNREADABLE <path>: <os error>` in place of its count and the exit is `1`. The last line is always one of `clean`, `REVIEW REQUIRED — N items`, or `FAILED — <first error>`, so a model reading only the tail still knows what to tell its human.

---

## 7. Checkpoints (gitter commits; the user reviews)

| # | after | user reviews | gitter commits |
| --- | --- | --- | --- |
| 1 | M1 | the status vocabulary (§ 3.4) as printed by `pfm professor check` on a fixture; `codexgen` test-mapping list | yes |
| 2 | M2 | one `pfm update --dry-run` report against a fixture store with a held file | yes |
| 3 | M3 (**before** Task 3.4's commit) | the Task 3.2 disposition table (amend any row), the `git diff --stat` of generated trees, the `leak-check` output | only after approval |
| 4 | M4 | `ls ~/.claude/commands ~/.claude/agents` and one demotion demo (`scope.project: ["commands/git.md"]` on a throwaway project → global `git.md` removed, reported) | yes |

Each checkpoint report: files touched, the fence line, the suite's quoted tail, and the exact open questions (or "none"). The orchestrating session runs the walker on the branch at checkpoints 1 and 3; merge to `main` after checkpoint 4 with `STATE.md` union; then the host mirror build. No push, no tag.

---

## 8. Out of scope (named so nobody "helpfully" does it)

- Detokenizing `quality/prompt.md`, `wave/refine.md`, `wave/walker.md`, `dev.md` to global scope — next wave, listed in the M4 report.
- Any change to `dreamer/`, `engines/wave-walker/engine/`, `harvester`, `pfm/internal/store` schema.
- A UI for overrides, an interactive merge tool, or a web view — the JSON and the report are the UI.
- `skills/sources.json` source-fetched skills — unchanged law.
- Releasing: `/ptm:release` sequence adjusts only in what it deletes (Task 3.1.c); `VERSION`/`CHANGELOG` discipline unchanged.
- `/h:*` — does not exist in this repo (§ 1.6); nothing to do.

---

## 9. Stop and report when

- A prerequisite in § 0.1 is missing.
- A template's token is absent from `docs/PLACEHOLDERS.md` (the human registry must be updated by `/ptm`, not by you inventing a token).
- Task 3.2 finds a live/blueprint difference none of the three classes (i)/(ii)/(iii) fits — quote the hunk.
- Task 3.4 step 1 exits non-zero — quote the report; do not edit a generated file to make it pass.
- An engine-contract seam you need (`engine.All()`, a descriptor field) does not exist as that spec describes — quote the grep.
- Anything this document does not cover.

Report shape (every time): `STOP: <one line>` + the quoted evidence + what you had finished (task numbers) + the fence line of the last green run.
