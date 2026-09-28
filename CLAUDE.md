# Professor — the discipline layer for Claude Code

This repo is the framework, not an app that uses it: everything under `templates/` is shipped source, one clone away from an adopter's live agent prompts, so every prompt line is production code.

# Vocabulary

- machine-global tier: agents, commands and skills whose original lives in this clone, symlinked into `~/.claude/{agents,commands,skills}` by `pfm install`; nothing generated is written into the clone · `templates/global/`
- project tier: templates `pfm init` scaffolds into an adopter once and pins in `{adopter}/.professor/baseline.json`; the adopter's local file is the truth, and pfm never rewrites it after init · `templates/project/`
- project-tier update: `pfm doctor --project-updates` reports `UPDATED / NEW / GONE-UPSTREAM / LOCAL-DELETED`, each `UPDATED` with its upstream diff; the adopter's session ports what applies, keeping its own edits, then `pfm update {adopt|pin|ignore|drop}` · `pfm/internal/update/`
- placeholder: the registered token for a project-specific value in a template · `docs/PLACEHOLDERS.md`
- specs: the framework's philosophy and generation law · `docs/BLUEPRINT.md`, `docs/SETUP.md`
- design docs: one directory per family, indexed by its `_index.md` · `docs/design/{family}/`
- refresh: the `/pfm:release prepare --from {live-project}` pass re-deriving `templates/project/**` from a live project; without it, hand-authored template edits ship as they are · `templates/refresh-map.json`, `scripts/refresh-scope.sh`, `scripts/genericize.sh`
- public face: the files a visitor or adopter reads first · `README.md`, `INSTALL.md`, `CHANGELOG.md`, `VERSION`, `releases/v{X.Y.Z}.md`
- leak gate: the identifying-content scan, run `pre-push` · `scripts/leak-check.sh`, `.githooks/pre-push`
- pfm: the fleet engine, Go · `pfm/cmd/pfm/`, `pfm/internal/` · child `pfm/CLAUDE.md`
- fleet prompt: the main chat's system layer per engine, composed at build into the tracked `pfm/harness-prompts/composed/` by `make -C pfm prompts`; sub-agents never receive it, so their first move and the dispatch law live under § Rules here · `pfm/harness-prompts/` · model tiers in § Model Selection `pfm/harness-prompts/share/head.md` · the main chat's rungs in § Orchestration `pfm/harness-prompts/share/tail.md`
- host assets: the files `pfm install` stages onto the host, owned here alone · `pfm/internal/installer/assets/`
- harvester: the only web and document harvester, over a pinned Python conversion sidecar · `pfm/internal/harvest/`, `pfm/internal/harvestmcp/`, sidecar `pfm/internal/harvestpy/`
- general family: `general-orchestrator` and its executors, for a clear batch · `templates/global/agents/` · design `docs/design/general/`
- flights: the spec → execute → land pipeline for large work, `/flights:*` with the `flights-*` agents · `templates/global/commands/flights/` · design `docs/design/flights/`
- flight directory: a flight's task files and audit trail, outside the tree and kept across reboots; never scratch · `$HOME/.local/state/pfm/flights/{project}/{flight}/`
- quality laws: the `/quality:*` family every prompt and orientation-file edit loads · `templates/global/commands/quality/` · design `docs/design/quality/`
- fence: the isolated dev container — fresh machine, own HOME, worktree mounted — with the `pfm-dev` image and the `pfm-sim` real-browser target · `infra/fence/` · design `docs/dev/isolated-dev-foundation.md`
- demo fence: for presentations · `infra/demo/`
- self-hosted manifest: the tracked ledger of this repo's own install, verified (restamped with `--write`) by a repo gate · `.professor/manifest.json`, `infra/check-self-hosted-manifest.sh`
- retro inbox: the steering ledger `/pcm retro` folds · `.professor/retro.md`
- this repo's install: the project tier this repo runs, source of truth for its engine mirrors; machine-global originals reach it only through `~/.claude/` symlinks, never a local copy · `.claude/`
- guard: the PreToolUse hook gating `.claude/**` and every `CLAUDE.md`, never a generated `AGENTS.md` · `.claude/scripts/pfm-guard.sh` · design `docs/design/hooks/`
- engine mirrors: `AGENTS.md`, `.codex/**`, `.opencode/**`, untracked, so a fresh clone generates before it checks · generated from `CLAUDE.md` and `.claude/` by `pfm codex build .` and `pfm opencode build .`
- Codex keeper: the one hand-written, tracked file under `.codex/` · `.codex/config.toml`
- marketplace: the plugin listing · `.claude-plugin/marketplace.json`
- CI: GitHub Actions gating pushes and pull requests · `.github/workflows/`, `.github/dependabot.yml`

## Path vars

- `$CDOCS`: `docs/commands`
- `$REFS`: `references`
- `$RESEARCH`: `research`
- `$RESOURCE`: `resource`

# Runtime

## Claude

- `CLAUDE.md` is the one hand-edited orientation file; `AGENTS.md` is compiled from it, never hand-edited, never a symlink; each engine's adapter translates mechanics, never identity or protocol.
- Every `.claude/agents/*.md` role compiles for Codex and OpenCode.
- Saving a machine-global template is the deploy (symlink-live); on this host `~/.professor` is this checkout.
- The `Stop` hook (`.claude/scripts/codex-sync.sh sync`) recompiles and checks both engine mirrors; after a Bash-driven write bypassed it: `pfm codex build . && pfm codex check .` then `pfm opencode build . && pfm opencode doctor .`

## Codex

- Reads `AGENTS.md`, whose § Codex adapter comes from `.claude/codex-build.json`, and `.codex/`.
- `pfm codex build .` is the single writer of its mirror; `pfm codex check .` its check.
- A role is a `.toml` twin `pfm install` (or `pfm codex agents`) writes as a marker-owned regular file into every Codex home's `{codex-home}/agents/` — Codex refuses a symlinked role — so a role changes at the next `pfm install`.
- `templates/project/scripts/build-codex.mjs` builds an adopter's mirror only.

## OpenCode

- Reads the same `AGENTS.md` (preferred over `CLAUDE.md`) plus `.opencode/`: `agent/*.md`, flat `/name` commands, skill symlinks, and `opencode.jsonc` pinning the guarded-file and non-gitter Git-write denies.
- `pfm opencode build .` is the single writer; `pfm opencode {check|doctor} .` its checks.

## Local

- On the host, `.claude/scripts/dev.sh {status|install|build|typecheck} {templates|pfm}` only; `verify`, `test`, `cover` and `all` refuse outside the fence.
- Scratch lives in `/tmp/{project}/{purpose}/`: `{project}` is this repo's directory name minus any leading dot (`.professor` → `professor`), derived, never hardcoded.
- One scratch subdirectory per purpose, owned by its protocol (`/tmp/{project}/{timing|lanes|guard}/`); a run never dirties the checkout; a scratch path named to a human or a model is absolute.

## Fence

- `.claude/scripts/dev.sh iso {install|build|typecheck|verify|test|cover|all|status|e2e|shell} [project]` or `iso {run|sim} {command…}` runs in the fence against a worktree under `.worktrees/{flight}/`; it needs a reachable docker daemon.
- `/pfm:release` drives `infra/fence/release-rehearsal.sh`.

## CI

- `.github/workflows/{verify|install-verify|plugin-scan}.yml` run on every push to `{main|develop}` and every pull request; `.github/workflows/release.yml` on a `v*` tag.

## Host

- After a fenced flight lands on `develop`: `make host-install` from `pfm/`, then `pfm install --yes`.

# Rules

## Publication (this repo's sacred ground)

- **No push, tag or release without an explicit request in the current turn.** A finished task, a green build, a "finish it" or a completed release document is NEVER permission; the authorized Git writer publishes only on the user's plain ask in that turn.
- **`main` is release-only:** all work lands on `develop`; `main` moves only when `/pfm:release` merges the `develop → main` release PR (gitter Phase RELEASE) — GitHub's ruleset and `.githooks/pre-push` refuse every other route.
- **Nothing identifying ships:** no source-project brand, user PII, client domain content or machine-absolute path (`/home/…`, `/Users/…`) in any tracked file; write it clean the first time — `scripts/leak-check.sh` is the backstop, not the plan.
- **Template example values are invented placeholders**, NEVER mined from a live private repo.
- **Version discipline:** `VERSION`, `CHANGELOG.md`, `releases/v{X.Y.Z}.md` and the tag MUST agree; between releases `develop`'s `VERSION` is the next `{X.Y.Z}-alpha`; `/pfm:release` owns the sequence.
- **Release notes are NEVER written during development:** `/pfm:release` derives them from `develop`'s diff against `main` and its commit messages.

## Your first move as a sub-agent

Before your first tool call, count the tasks in your brief. A task is one deliverable with its own files and its own acceptance check; items landing in the same file or the same small module are one task, however many bullets list them.

- A brief naming a task file: open it together with the shared files named beside it, in your first message, and execute it.
- A brief carrying the user's ruling to skip the ceremony (no `flights-speccer`, no orchestrator): do it yourself, start to finish, whatever its size.
- Otherwise take the lowest rung that fits; a higher rung needs its named reason. You finish within 45 calls; at the cap, return what landed, what is left and the next step.
  1. The solution is in hand and fits about 80 calls, a small failure you can read to its cause included: do it yourself when it fits your 45 calls, otherwise one or two sub-agents, in sequence or in parallel.
  2. The solution is in hand but the volume is past one or two agents — many clear tasks with nameable files: your first call spawns `general-orchestrator` with the work, all you hold and the check that proves the batch done.
  3. The solution is not in hand — a design to choose, a failure of unknown cause, files you cannot name — and the work is large: your first call spawns `flights-speccer` with the work, all you hold and a directory under `$HOME/.local/state/pfm/flights/{project}/`. Its return is your orders: one task file, you execute it; several, you execute none and hand the directory to `flights-orchestrator`. Below the smart tier you write no spec yourself.
- ✓ "Fix these five things in `ledger.mjs` and update its README" is one task: read it, fix it, test it — no spawn.
- ✓ "Add the timeout flag to each of the 12 subcommands" is a batch of clear tasks: `general-orchestrator`, no `flights-speccer`.
- ✗ "Take the four failing test lanes to green" done by one agent: hundreds of calls, each re-sending a context past 400K. ✓ `flights-speccer`, then `flights-orchestrator`.
- Waiting is one call: an explicit `timeout` up to the maximum, or one blocking wait — never a no-op command, a repeated log peek or a `sleep` chain.

## Dispatch

Tiers and effort live in the fleet prompt's § Model Selection; the cast and each agent's pinned model in `.claude/agents/` frontmatter.

- Every dispatch carries all five: the goal in one sentence and the artifact it returns, its shape named; the boundary, in scope and explicitly out; the anchors — exact files, symbols or commands, never "find the relevant code"; the tier and effort, plus a budget when the task can run away; what its own failure looks like — a dead end, an empty result, a tool that would not run.
- All sibling agents of a round go in one message; agents dispatched and reports received are counted and must match, and a missing report is a named coverage hole.
- An invariant enforced across layers (Go + shell + prompt) is `tracer`-mapped closed-world before the build dispatch; the spec carries every enumerated door — an invariant held at N−1 of its N doors is a violation at the missing door.
- Agent reports are evidence, not truth: verify a claim against what you can read yourself before relaying it.

## Prompt & template code

- A template is the live source file, verbatim — same structure, mechanics, character and logic; only project-specific values swap for placeholder tokens; the prose is never abstracted, skeletonized or genericized.
- One canonical token per concept — never a synonym for a registered placeholder.
- No dangling pointers: grep before you cite; a pointer to a file, agent or command the install does not produce is deleted, or its target ships.
- Every check names what its own broken state reports: a gate answering "fine" both healthy and broken is a coincidence detector.
- An error never renders as absence: "nothing there" and "we failed to look" read differently at the visible surface; logging alone is not sufficient.
- The judge is never the thing being judged: read the artifact from disk, never a verdict asserted in a brief; an empty enumeration is clean only once the enumerator provably ran, and the parent reports which.
- Surgical changes: every changed line traces to the task; fix broken things you hit; remove dead code, references and deps end to end, including `README.md`, `docs/BLUEPRINT.md`, `docs/SETUP.md` and `templates/refresh-map.json`.
- No duplication: grep for the existing rule, section or script and reference it; a near-copy drifts.
- Twins move together: a `.claude/**` change any adopter could use lands in its `templates/project/**` twin in the same pass, the commit message carrying the adopter-facing change; a customization only this repo wants stays local; unsure → ask.
- Right-size and finish: the simplest thing that works, no speculative abstractions, no stubs or deferred TODOs; where correct and convenient diverge, take correct, even at re-architecting cost.

## Engine code (Go / TS / JS / Python)

- Every `catch` and `if err != nil` logs full context and handles or returns the error.
- Validate at data entry.
- Follow the package's existing naming and structure; a new directory or pattern only when the task requires it.
- Install only validated libraries.

## Process

- Git writes go through registered `gitter`; every other sub-agent is read-only.
- When `gitter` is unavailable, only the active main Codex chat performs scoped Git writes, and only after the user's explicit authorization in the current turn; publication still needs its own request under § Publication.
- Commit only code whose tests pass.
- Guarded files: a task touching `.claude/**`, any `CLAUDE.md` (the source of its `AGENTS.md`) or `templates/**` routes to `/pcm`; the guard admits its files only in a session that has read `~/.claude/commands/quality/prompt.md`, and its deny message carries the unlock steps.
- Edits to `.claude/**` and any `CLAUDE.md` (never its generated `AGENTS.md`) are the main chat's, under `/pcm`; a sub-agent reports the change it needs.
- Disabling the guard hook, or routing a sub-agent around it, is a violation.
- Code flights build inside the fence: a git worktree under `.worktrees/{flight}/`, every build and test through `.claude/scripts/dev.sh iso`; an executor runs only its affected tests per `.claude/commands/pfm-testing-manual.md`; the full suite is the gate's.
- Dev runs target fence worktrees; the live checkout, the host's `~/.local/bin` and the real `$HOME` stay untouched.
- Markdown-only flights (templates, docs, prompts) land on `develop` directly.
- A fenced flight closes in order: one `flights-lander` per project (checks, one review of the whole diff, adversarial tests, its own fixes) → the landing's checks → the authorized Git writer commits to `develop` → the host mirror build under § Host.
- At every milestone, checkpoint the plan to a file, then compact before the next phase; a held turn arms an idle-fired self-inject instead.
- AskUserQuestion is the user's whole screen: context travels inside the question text; each round simpler and more concrete, never a rephrase.

## Testing

- **Every test runs inside the fence, never on the host:** the gate is `.claude/scripts/dev.sh iso test {templates|pfm}`, run before claiming anything works; never report a suite you did not watch run.
- A regression test counts only after it was watched failing against the unfixed code.
- A skipped or filtered suite is a named gap in the report, never a pass.
