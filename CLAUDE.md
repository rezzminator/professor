# Professor — the discipline layer for Claude Code

## Spawned as a sub-agent? Your first move

Before your first tool call, count the tasks in your brief. A task is one deliverable with its own files and its own acceptance check; items landing in the same file or the same small module are one task, however many bullets list them.

- One task you can see how to do — the brief plus one look at the target tells you which files change and how: do it yourself, start to finish.
- One task you cannot see how to do — a failure with an unknown cause, a design to choose, files you cannot name: your first tool call spawns an `opus` scout under you (Agent tool, `model: opus`) that investigates and returns the spec — the change and its acceptance check, never pre-written code — then you execute it. Below opus you never write that spec yourself.
- A batch of several tasks: you execute none. The `opus` scout returns one spec per task; you spawn one agent per task under you, its spec pasted into a self-contained brief — the excerpt itself, never a pointer to a document; tasks sharing a lock, database, port or branch run in order, the rest together in one message.
- ✓ "Fix these five things in `ledger.mjs` and update its README" is one task: read it, fix it, test it — no scout, no spawn.
- ✗ "Take the four failing test lanes to green" done by one agent: hundreds of calls, each re-sending a context grown past 400K. ✓ The `opus` scout first, then one agent per lane.
- Waiting is one call: an explicit `timeout` up to the maximum, or one blocking wait — never a no-op command, a repeated log peek or a `sleep` chain.

**What this repo is:** the framework itself, not an app that uses it. Everything under `templates/` is **shipped source** — an adopter's live agent prompts, one clone away. Treat every prompt line as production code, because it is.

## Repo structure

- `templates/`: the shipped framework — `templates/project/` (per-install templates, scaffolded into an adopter and then owned there) + `templates/global/` (machine-global agents/commands/skills — the originals, symlink-live) + `templates/prompts/` (the fleet prompt + harness baseline). Markdown + shell, no build; the gates are `scripts/leak-check.sh` and `scripts/refresh-scope.sh`.
- `pfm/`: fleet engine — Go 1.24, `cmd/pfm` + `internal/*`. Owns its staged host assets under `pfm/internal/installer/assets/`; `pfm install` stages them. Also owns the only harvester under `internal/harvest` + `internal/harvestmcp`, over a pinned Python conversion sidecar in `internal/harvestpy/`.
- `workflows/`: in-tree engines — `deep-rr/` (the USER-ONLY research workflow), linked into `~/.claude/skills/` by `pfm install`.
- `infra/`: the isolated-dev fence (`docker-compose.yml`, the `pfm-dev` image, `release-rehearsal.sh`, `check-self-hosted-manifest.sh`) — `dev.sh iso` and `/pfm:release` drive it.
- `docs/`: the specs — `BLUEPRINT.md` (philosophy), `SETUP.md` (generation), `PLACEHOLDERS.md` (substitution law) — plus `commands/` reference cards and `dev/` wave trains.
- `scripts/`: repo-level gates (`leak-check.sh`, `refresh-scope.sh`); `.githooks/` runs the leak gate `pre-push`.
- `releases/` + root `README.md` / `INSTALL.md` / `CHANGELOG.md` / `VERSION`: the public face — edited with template-grade care.
- `.claude/`: this repo's own project-tier install — the commands, agents, skills, and scripts THIS repo uses, the source of truth for its mirrors. Machine-global originals reach it through `~/.claude/` symlinks; where one needs this repo's anchors (`tracer`, `scheduler`, `quality/*`, `wave/*`) a rewired local variant lives here, logged in `drift.md`. `.codex/` and `.opencode/`: pointer layers compiled over it, never a restatement.
- `.professor/`: ledgers — `drift.md` (this install's keep-local customizations; never consumed), `retro.md` (steering inbox; `/pcm retro` folds it). Release notes are never written during development: `/pfm:release` derives them from `develop`'s diff against `main`.
- `tmp/`: gitignored scratch — every generated artifact lands here, never in a tracked dir.

Build/test through `.claude/scripts/dev.sh {status|install|build|typecheck|verify|test} {templates|pfm}`.

## How the framework reaches an adopter — three tiers, one truth each

- **Machine-global** (`templates/global/`): truth is the original in the blueprint clone. `pfm install` symlinks each original into the engine registries (`~/.claude/{agents,commands,skills}`); `pfm codex agents` compiles the Codex `.toml` twins beside their `.md` and links them into `~/.codex/agents/`. Saving a template IS the deploy — on this host `~/.professor` is this checkout.
- **Project** (`templates/project/` → an adopter's `CLAUDE.md`, `.claude/**`, `docs/`, `.codex/` keepers): truth is the adopter's local file, full stop. `pfm init` scaffolds once and pins every file in `.professor/baseline.json`; `pfm update adopt [--at REF]` pins an install that predates scaffolding. `pfm update check` reports `UPDATED / NEW / GONE-UPSTREAM / LOCAL-DELETED`, each with the exact `git diff` to read; the adopter's session hand-applies what belongs, then `pfm update pin` (accept) / `ignore` (never adopt) / `drop` (forget). pfm never rewrites a project file after init.
- **Engine mirrors** (`AGENTS.md`, `.codex/**`, `.opencode/**`): generated from the project's Claude sources by `pfm codex build|check` and `build-opencode.mjs`; never hand-edited.

The reverse direction is the release: `/pfm:release` sends reviewers over `develop`'s diff against `main` (commit messages included) to write `releases/vX.Y.Z.md` + `CHANGELOG.md` and every adopter instruction; with `--from {live-project}` its refresh pass re-derives `templates/project/**` from that project's live files per `templates/refresh-map.json` (`scripts/refresh-scope.sh` + `scripts/genericize.sh`); without it, hand-authored template edits ship as they are.

## Three-runtime team — Claude + Codex + OpenCode

`CLAUDE.md` and `AGENTS.md` are one shared contract; runtime wrappers translate mechanics, never identity or protocol. `AGENTS.md` is **compiled** from this file — never hand-edited, never a symlink; edit `CLAUDE.md` and the `Stop` hook recompiles both mirrors. OpenCode reads the same compiled `AGENTS.md` (its loader prefers it over `CLAUDE.md`) plus its own `.opencode/` layer, compiled by `build-opencode.mjs`: agents (`.opencode/agent/*.md`), commands (`/flat-name`), skill symlinks, and `opencode.jsonc`, where guarded-file and non-gitter Git-write denies remain pinned. Every `.claude/agents/*.md` role compiles for Codex and OpenCode; only registered `gitter` retains Git-write authority. The active main Codex chat may use the user-authorized fallback under § Process when gitter is unavailable. After a Bash-driven write bypassed the hook:

```bash
pfm codex build . && pfm codex check .
node .claude/scripts/build-opencode.mjs generate && node .claude/scripts/build-opencode.mjs doctor
```

`pfm codex build` is the SINGLE writer of the Codex mirror; the legacy repo-local JS compiler is retired — `templates/project/scripts/build-codex.mjs` lives only in the adopter blueprint.

## Path vars

`$CDOCS`: `docs/commands` · `$REFS`: `references` · `$RESEARCH`: `research` · `$RESOURCE`: `resource`

## MANDATORY Rules

### Publication (this repo's sacred ground)

- **No push, tag, or release without an explicit request in the current turn.** A finished task, a green build, a "finish it", or a completed release document is never permission to publish. The authorized writer publishes only on the user's plain ask in that turn.
- **`main` is release-only.** All work lands on `develop`; GitHub's ruleset (pull request + green checks required, no force-push, no deletion) and `.githooks/pre-push` both refuse a direct push to `main`, which moves only when `/pfm:release` merges the `develop → main` release PR (gitter Phase RELEASE).
- **Nothing identifying ships:** no source-project brand, no user PII, no client domain content, no machine-absolute path (`/home/…`, `/Users/…`) in any tracked file. `scripts/leak-check.sh` (`pre-push`) is the backstop, not the plan — write it clean the first time.
- Template example values are invented placeholders, never mined from a live private repo.
- **Version discipline:** `VERSION`, `CHANGELOG.md`, `releases/vX.Y.Z.md`, and the tag agree or the release is wrong; between releases `develop`'s `VERSION` is the next `X.Y.Z-alpha`. `/pfm:release` owns the sequence.

### Prompt & template code

- **A template IS the live source file, verbatim** — same structure, mechanics, character, logic; only project-specific values swap for `docs/PLACEHOLDERS.md` tokens. Never abstract, skeletonize, or "genericize" the prose.
- One canonical token per concept — never a synonym for a registered placeholder.
- **No dangling pointers:** grep before you cite; a referenced file/agent/command the install does not produce = delete the pointer or ship the target.
- **Every check names what its own broken state reports.** A gate answering "fine" when healthy AND when broken is a coincidence detector. An error never renders as ABSENCE — absence claims "nothing there", an error claims "we failed to look"; distinguish them at the visible surface, logging alone is not sufficient.
- **The judge is never the thing being judged:** read the artifact from disk, never trust a verdict asserted in a brief; an empty enumeration is clean only once the enumerator provably ran.
- Surgical changes: every changed line traces to the task; fix broken things you hit; dead code/references/deps — remove entirely, end to end (including `README.md`, `BLUEPRINT.md`, `SETUP.md`, `refresh-map.json`).
- NO duplication: grep for the existing rule/section/script and reference it; never keep a near-copy that will drift.
- **Twins move together:** a `.claude/**` change any adopter could use lands in its `templates/project/**` twin in the same pass, its commit message carrying the adopter-facing change; a customization only this repo wants logs to `drift.md`. Unsure → ask.
- Right-size and finish: simplest thing that works, no speculative abstractions, no stubs or deferred TODOs.

### Engine code (Go / TS / JS / Python)

- Never swallow exceptions — every `catch` / `if err != nil` logs full context.
- Validate at data entry; an `as`-cast blinds `tsc` to the nullability that crashes on the first real row.
- Follow the package's existing naming and structure; new dirs/patterns only when the task requires them.
- Never install unvalidated libraries.

### Process

- **Git writes use registered gitter.** Every other subagent is read-only. When gitter is unavailable, only the active main Codex chat may perform scoped Git writes, and only after explicit user authorization in the current turn. Publication still requires the separate explicit in-turn request above.
- **Never commit broken code** — tests pass before the commit.
- **Code waves build inside the fence** — a git worktree under `.worktrees/{train}/`, every build/test through `dev.sh iso` (the `infra/` container: fresh machine, own HOME, worktree mounted; design: `docs/dev/isolated-dev-foundation.md`). The live checkout, the host's `~/.local/bin`, and the real `$HOME` are never dev targets. Markdown-only waves (templates/docs/prompts) land on `develop` directly. A fenced wave closes in order: QA pass → orchestrator review with issues fixed → authorized Git writer merges to `develop` → the host mirror build (`make host-install` from `pfm/` + `pfm install --yes`). The installed wave commands (`/wave:refine`, `/wave:live`, `/wave:walker`, `/wave:ccc`) are rewired to this cast — `dev` builds, `qa` tests, `gitter` commits and merges; a task touching `.claude/**`, any `CLAUDE.md`, or `templates/**` routes to `/pcm`. Their `templates/project/commands/wave/` twins keep the adopter pipeline.
- **Guarded files:** a PreToolUse hook gates `.claude/**` and every `CLAUDE.md` behind `/pcm` plus a session that has read `.claude/commands/quality/prompt.md`; the deny message carries the unlock steps. Never route around it by disabling the hook.
- **Milestone = compact point:** at every milestone, checkpoint the plan to a `tmp/` file, then give yourself a compact before the next phase (a held turn arms an idle-fired self-inject instead).
- **AskUserQuestion is the user's whole screen** — context travels inside the question text; each round simpler and more concrete, never a rephrase.
- When in doubt, do the right thing — correct over convenient, even at re-architecting cost.

### Testing

- Run the project's own gate before claiming anything works: `.claude/scripts/dev.sh test {project}` — never report a suite you did not watch run.
- **A regression test counts only after it was watched FAILING against the unfixed code.**
- A skipped or filtered suite is a NAMED gap in the report, never a pass.

## Subagent dispatch

Tiers, effort, and delegation posture live in the fleet prompt's § Model Selection — never restated here. The cast, its triggers, and each agent's pinned model live in the harness registry (`.claude/agents/` frontmatter, injected every session).

**The briefing contract — every dispatch carries all five:**

1. The goal in one sentence, and the artifact it must return (a path, a map, a verdict — name the shape).
2. The boundary — what is in scope and, explicitly, what is NOT.
3. The anchors — exact files, symbols, or commands to start from; never "find the relevant code".
4. The tier and effort per the fleet prompt's § Model Selection, plus a budget when the task can run away.
5. What its own failure looks like — how to report a dead end, an empty result, a tool that would not run. Silence is never a result.

**The laws:**

- Sync-dispatch: all sibling agents of a wave go in ONE message; a missing report is a loud, named coverage hole.
- Map before dispatch: an invariant enforced across layers (Go + shell + prompt) is tracer-mapped closed-world BEFORE the build dispatch; the spec carries every enumerated door, never "find the rest" — an invariant enforced at N−1 of its N doors is a violation at the missing door.
- An empty enumeration is never a verdict: "looked and found nothing" ≠ "failed to look" — the parent reports which.
- Reconcile telemetry: agents dispatched vs reports received must match, and the count appears in the report.
- **Only gitter writes git; no subagent edits `.claude/**` / a `CLAUDE.md`** — the guard denies those framework edits; routing around it is a violation, not initiative.
- Agent reports are evidence, not truth — verify a claim against what you can read yourself before relaying it.
