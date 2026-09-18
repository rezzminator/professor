# Scaffold-and-own: `pfm init` deploys project templates ONCE and pins per-file baselines; local files own truth; `pfm update` reports upstream template diffs for reviewed, hand-applied adoption

Status: REFINED · Ruled: 2026-09-01 by the owner · Project: pfm + templates + docs · One worktree, NO train · Written by the first adopter's main session on the owner's order; P:BUILDER executes.

**SUPERSEDES** the regeneration/override architecture of `2026-08-23-blueprint-compiler.md` (its rulings 1–2 are re-ruled by the owner 2026-09-01). What DIES from that spec: the `pfm professor build` pipeline (M1), the override-engine v2 middleware, `.professor/overrides/**`, `placeholders.json`/`templates.json`/directive machinery, project re-materialization on update (M2's build half), repo inversion (M3), and M4's demotion/`global-build.json` remainder. What STANDS, untouched: the engine registry (`pfm/internal/engine/`), machine-global symlink registration (`codexgen/globallink.go`, landed @ `5b87cf3`), the two-scope store layout (`templates/{global,project}` — landed), `pfm codex build|check` as the engine-mirror compiler reading LOCAL files as source, the `/pfm:install` interview concept (implemented as the blueprint `docs/SETUP.md` § Install interview — no slash command ships; ruled by W:CCC 2026-09-01), and `pfm update`'s existing machine half (source-clone semver update → binary rebuild → `install --yes` → doctor).

## The ruling (2026-09-01, verbatim intent)

> "We do pfm install on a project, it deploys/transforms its templates there, and then with next updates, model/I do pfm update; in the update it looks at which of its templates has changed, and it writes that there's a new version of some command/agent/skill, and the model will check the diff and updates it manually."

Ratified with one professor-added requirement: **per-file baseline pins** recorded at deploy time — without them, update cannot separate upstream change from local customization. Model: cookiecutter-cruft / Arch `.pacnew`. Three tiers, each with its own truth rule:

| tier | truth | mechanism |
| --- | --- | --- |
| machine-global | blueprint original | symlink-live (`globallink.go`) — a template save IS the deploy; unchanged |
| project (`.claude/**`, `CLAUDE.md`, scripts, skills, workflows) | **the local file, full stop** | scaffolded ONCE by `pfm init`; upstream deltas arrive as *reported diffs* the session reviews and applies by hand |
| engine mirrors (`.codex/**`, `AGENTS.md`, opencode) | generated, never hand-edited | `pfm codex build|check` compiles from the LOCAL files; unchanged |

`pfm` NEVER writes a scaffolded project file after init. No regeneration, no override files, no hold-and-merge. Update output is a report; the applying hand is the session's, per-file, judged.

## `.professor/baseline.json` — the pin file

Machine-written by pfm only, **tracked in git** (a clone must know its baseline). The user-owned interview stays in `manifest.json`; the two never share a writer.

```json
{
  "version": 1,
  "blueprint": { "version": "0.56.0", "sha": "ff7460e" },
  "files": {
    ".claude/commands/dev.md": {
      "template": "project/commands/dev.md",
      "templateHash": "sha256:…",
      "pinnedSha": "ff7460e",
      "pinnedAt": "2026-09-01"
    }
  }
}
```

- Key = local path relative to project root. Many locals MAY pin the same template (roster expansion).
- `templateHash` = sha256 of the TEMPLATE file bytes at pin time, **tokens intact** — the interview's local fill never disturbs a pin, and the update diff is template-vs-template.
- `pinnedSha` = blueprint short sha at pin time — the convenience anchor for `git -C ~/.professor diff <pinnedSha>..HEAD -- templates/<template>`.
- `blueprint.sha` advances only via `pin`; per-file pins advance individually (accept one file today, defer another — the deferred file stays flagged next run).

## Statuses (the whole vocabulary; absence-vs-error law binds every probe)

| status | meaning | report action line |
| --- | --- | --- |
| `current` | template hash matches pin | (counted, not listed) |
| `UPDATED` | upstream template changed since pin | `review: git -C <blueprint> diff <pinnedSha>..HEAD -- templates/<template>` then apply by hand and `pfm update pin <local>` |
| `NEW` | `templates/project/**` file with no pin targeting it | `adopt: copy/adapt it locally, then pfm update pin --template <template> <local>` — or ignore (stays listed) |
| `GONE-UPSTREAM` | pinned template no longer exists upstream | `local file is YOURS now — keep it and pfm update drop <local>, or delete both` |
| `LOCAL-DELETED` | pin exists, local file does not | `pfm update drop <local>` to forget, or restore the file |
| `UNREADABLE` | store/baseline/template could not be read (not ENOENT) | abort with path + OS error, exit `1` — never folded into "current" |

Exit codes: `0` all current · `3` anything to review (UPDATED/NEW/GONE-UPSTREAM/LOCAL-DELETED > 0) · `1` error. Last line always one of `clean` / `REVIEW REQUIRED — N items` / `FAILED — <first error>` so a model reading only the tail knows what to tell its human. Counts print even at zero.

## Tasks

Go 1.24, `pfm/internal/*` conventions, no swallowed errors, tests beside code with `t.TempDir()` — never the host `$HOME` (jail guard enforces). Build/test ONLY inside the fence: `./.claude/scripts/dev.sh iso test pfm` / `iso verify pfm`; quote the `fence: container=… HOME=/root work=/worktree` line in every report — a report without it is a report of an unfenced run. Never run state-changing git; gitter alone commits, at the two checkpoints. Never push, tag, or release. Worktree: `.worktrees/scaffold-and-own/` (gitter creates it first; missing → `PREREQ MISSING`).

### T1 — package `pfm/internal/professor`: baseline load/save + template hashing

- `baseline.Load(root)` / `Save` for the § schema; a malformed file is a named error, never an empty baseline; version ≠ 1 → `BASELINE-VERSION {n}: unsupported`.
- `HashTemplate(storePath)` — sha256 over exact bytes. Store resolution: self-hosted = the blueprint clone (`manifest.interview.blueprint_clone_path`, default `~/.professor`), `templates/` subtree; short sha via `git rev-parse --short HEAD` there (missing `.git` → `self-hosted@unknown`, reported, never fatal).
- File plan: `pfm/internal/professor/baseline.go`, `store.go`, each with `_test.go`.
- Tests (RED first): round-trip; malformed JSON named; unknown status never invented; a `000`-mode template file → `UNREADABLE` with OS error text, never "current".

### T2 — `pfm init` deploys + pins (extend `cmd/pfm/init_command.go`)

- Keep `runInit`/`discoverSourceRepo`; extend `initScaffold` (or replace its call site — builder's choice, behavior below is the contract): deploy `templates/project/**` **verbatim, tokens intact** into the target per this map: `commands/**→.claude/commands/`, `agents/**→.claude/agents/` (EXCEPT `agents/per-project/**` — interview-deployed, see below), `scripts/**→.claude/scripts/` (+x preserved), `skills/**→.claude/skills/`, `workflows/**→.claude/workflows/`, `settings.json→.claude/settings.json`, `CLAUDE.md→CLAUDE.md`, `codex/**→.codex/`, `docs-commands/**→docs/commands/**`, `docs-agents/**→docs/agents/**` (ruled by W:CCC 2026-09-01 — SETUP.md convention wins). A landed convention that contradicts this map → stop and report, don't improvise.
- `per-project/**` and token fill are the install interview's job (LLM, local files), implemented as the blueprint `docs/SETUP.md` § Install interview — no slash command ships; ruled by W:CCC 2026-09-01. pfm copies nothing from `per-project/` and substitutes nothing — the substitution engine does not exist in Go.
- Write one pin per deployed file; write `.professor/baseline.json`; print the deploy count + `open Claude here and follow <blueprint>/docs/SETUP.md § Install interview — it fills tokens and deploys per-project agents`.
- Existing-file collision: `--force` overwrites, default reports `CONFLICT {path}: exists` and skips (still pinned? NO — unpinned + listed, the interview decides).
- **Scaffold marker**, only where context-free (never a token the harness feeds the model): frontmatter files get a YAML comment as first line inside the fence — `# pfm-scaffold: {template}@{sha} — this file is YOURS; upstream deltas arrive via pfm update, reviewed and hand-applied`; `.sh` gets the same as line 2. NO marker in `CLAUDE.md`, `AGENTS.md`, or any file without a free comment slot — `baseline.json` owns provenance; a context-taxing marker is a bug.
- Tests: golden deploy of a fixture store; marker placement (first line still `---`); collision skip; pins written for exactly the deployed set.

### T3 — `pfm update` project report + `pin`/`drop` (extend `cmd/pfm/update_command.go`)

- Bare `pfm update`: existing machine flow unchanged, THEN — if CWD (or `--root DIR`) walks up to a `.professor/baseline.json` — append the project report per § Statuses.
- `pfm update check [--root DIR]` — the report alone: no fetch, no rebuild, no install. This is the everyday model-facing verb.
- `pfm update pin <local>... | --all [--root DIR]` — re-pin named files (or every UPDATED one) to the current template hash + sha. `pin --template <template> <local>` creates a new pin (NEW adoption, roster expansion).
- `pfm update drop <local>...` — forget a pin (GONE-UPSTREAM keep / LOCAL-DELETED).
- Report format (human default; `--json` one object):

```
professor: /path/to/project  blueprint ff7460e → 1a2b3c4
  current      38
  UPDATED       2
    .claude/commands/wave/orchestrator.md   project/commands/wave/orchestrator.md  pinned @ff7460e
      review: git -C ~/.professor diff ff7460e..1a2b3c4 -- templates/project/commands/wave/orchestrator.md
      then apply by hand and: pfm update pin .claude/commands/wave/orchestrator.md
  NEW           1   project/commands/quality/doc.md — adopt: … | ignore
  GONE-UPSTREAM 0
  REVIEW REQUIRED — 3 items; nothing was written.   exit 3
```

- `pfm doctor` gains one line per known project state when run inside one: `professor: current N · review-required N` (unreadable baseline = `UNREADABLE`, never "not managed").
- Tests: fixture blueprint as a throwaway git repo (reuse `update_command_test.go` helpers); UPDATED/NEW/GONE-UPSTREAM/LOCAL-DELETED each covered; `check` is side-effect-free (hash tree before/after); pin advances one file and leaves the deferred one flagged; exit codes 0/3/1.

**[CHECKPOINT 1]** — gitter commit `feat(pfm): scaffold-and-own — init pins baselines, update reports template diffs`. Report to the dispatching chat: fence line, suite tail, a fixture `pfm update check` output pasted verbatim.

### T4 — kill the unused override engine

- Grep consumers of `codexgen/override.go` (`ApplyOverrides`, `OverridesDir` in `config.go`, call sites in `compiler.go`) — remove the mechanism end to end: code, config key, tests, and every legacy override-directory mention in docs/templates. `pfm codex build|check` behavior on a repo WITHOUT overrides must be byte-identical before/after (prove: run both against this repo's mirror, diff empty).
- If any live consumer exists beyond the codex compiler's optional read → stop and report, do not delete.

### T5 — docs + supersession trail

- `INSTALL.md`, `docs/SETUP.md`, `docs/BLUEPRINT.md` § "Staying current": rewrite the install/update story to this spec's three-tier table + flow (init once → local truth → `pfm update check` → reviewed hand-apply → `pin`). Delete every remaining description of regeneration-on-update and LLM three-way merge.
- `templates/project/commands/pfm.md` § "Where a change lands": the shipped card now says — framework change → blueprint template (via release flow); project customization → **edit the local file directly** (it is the source); engine mirrors → never by hand, `pfm codex build`; upstream deltas → `pfm update check` review-and-apply. The "local stopgap" ceremony dies.
- Stamp `docs/dev/trains/queue/2026-08-23-blueprint-compiler.md` header: `**Status:** SUPERSEDED (scope named in 2026-09-01-scaffold-and-own-update.md; engine registry, globallink, two-scope store stand as landed)`.
- Changelog/VERSION per this repo's release discipline (entry under the pending release; no tag).

**[CHECKPOINT 2]** — gitter commit `docs(professor): scaffold-and-own supersedes regeneration — install once, own locally, update by reviewed diff`. Final report: files touched, fence line, both suite tails, the § Statuses fixture demo, open questions or "none".

## Out of scope (named so nobody "helpfully" does it)

- The first adopter's own `.professor/manifest.json` `files`/`_note` cleanup and its baseline seeding — the adopter's session does that at its next `/pfm` pass, not this build.
- `templates/refresh-map.json` / `scripts/refresh-scope.sh` — the blueprint's own release-time live↔template equalizer; untouched here.
- Global `.toml` twins, opencode, themes, skills `sources.json` law — unchanged here (Wave 9 later moved every generated mirror, twins included, out of version control entirely).
- Any UI/interactive merge tool — the report is the UI.

## Stop and report when

- A prerequisite is missing (worktree, fence, a named file absent).
- The T2 deploy map contradicts a landed convention in `templates/` or docs.
- `override.go` has a live consumer T4 didn't expect.
- Anything this document does not cover. Shape: `STOP: <one line>` + quoted evidence + finished task numbers + last green fence line.
