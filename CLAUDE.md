# Professor — the discipline layer for Claude Code

This repo is the framework, not an app that uses it: everything under `templates/` is shipped source, one clone away from an adopter's live agent prompts, so every prompt line is production code.

Where Vocabulary, Runtime or On demand disagrees with the code on a path, command or convention, follow the code and name each mismatch in your reply; `# Rules` binds regardless.

# Vocabulary

- machine-global tier: agents, commands, rules and skills symlinked into `~/.claude/{agents,commands,rules,skills}` by `pfm install`, except `templates/global/codex/config.toml` (Codex defaults merged into each Codex home, existing settings winning) and `templates/global/engine-sync.json` (engine-sync's policy, rendered with this host's targets into `~/.claude/engine-sync.json`); a `from:` declaration renders into `templates/generated/agents/`, the fence gate holding it equal to a fresh render · `templates/global/`, `templates/generated/README.md`
- project tier: templates `pfm init` scaffolds into an adopter once and pins in `{adopter}/.professor/baseline.json`; the adopter's local file is the truth, and pfm never rewrites it after init · `templates/project/`
- project-tier update: `pfm doctor --project-updates` reports `UPDATED / NEW / GONE-UPSTREAM / MOVED / LOCAL-DELETED`, each `UPDATED` with its upstream diff, plus a `RETIRED-NAME` section for retired references in project files, including CURRENT pins; non-CURRENT pins are left to their report rows; the adopter's session ports what applies, keeping its own edits, and closes every row with `pfm update {adopt|pin|ignore|drop|accept}` (`pin --keep` records a deliberate non-port, `accept` a retired-name hit that stays) until the report ends `clean` · `pfm/internal/professor/`, `pfm/internal/update/`
- host update: this host's pfm build, `pfm install` and plugin repoint, never the project-tier update · `.claude/commands/pfm/update.md`
- placeholder: the registered token for a project-specific value in a template · `docs/PLACEHOLDERS.md`
- spec: the adopter install interview and generation law · `docs/SETUP.md`
- design docs: one directory per family, indexed by its `_index.md` · `docs/design/{family}/`
- refresh: `pcm`'s pass re-deriving `templates/project/**` from a live project, run on request and detached from the release; without it, hand-authored template edits ship as they are · `$CDOCS/pcm/$REFS/refresh.md`, `templates/refresh-map.json`, `scripts/refresh-scope.sh`, `scripts/genericize.sh`
- public face: the files a visitor or adopter reads first · `README.md`, `INSTALL.md`, `CHANGELOG.md`, `VERSION`, `releases/v{X.Y.Z}.md`
- leak gate: the identifying-content scan, run `pre-push` · `scripts/leak-check.sh`, `.githooks/pre-push`
- pfm: the fleet engine, Go · `pfm/cmd/pfm/`, `pfm/internal/` · child `pfm/CLAUDE.md`
- fleet prompt: the main chat's system layer per engine, gitignored at `pfm/harness-prompts/generated/{engine}.md`; sub-agents never receive it, so the dispatch law lives under § Rules here · generated from `pfm/harness-prompts/` by `pfm install`
- host assets: the files `pfm install` stages onto the host, owned here alone · `pfm/internal/installer/assets/`
- harvester: the standalone web and document harvester, over a pinned Python conversion sidecar · `plugins/harvester/`
- flights: the foreman tiers and the lander that build and land work, and `/flights:doctor`, their meter · `templates/global/agents/flights-*.md`, `templates/global/commands/flights/` · design `docs/design/flights/`
- quality laws: the `/quality:*` family every prompt and orientation-file edit loads · `templates/global/commands/quality/` · design `docs/design/quality/`
- fence: the shared `pfm-dev` container, own namespaces, HOME and `/tmp` per call, and the `pfm-sim` browser target · `infra/fence/` · design `docs/dev/isolated-dev-foundation.md`
- demo fence: for presentations · `infra/demo/`
- self-hosted manifest: the tracked ledger of this repo's own install, verified (restamped with `--write`) by a repo gate · `.professor/manifest.json`, `infra/check-self-hosted-manifest.sh`
- this repo's install: the project tier this repo runs, source of truth for its engine mirrors; machine-global originals reach it only through `~/.claude/` symlinks, never a local copy · `.claude/`
- engine-sync: the single writer of every Codex and OpenCode artifact derived from a Claude source, a Go binary and Claude plugin; pfm execs it, and `pfm install` links it as `~/.local/bin/engine-sync` · `plugins/engine-sync/` · design `docs/design/engine-sync/`
- engine mirrors: `AGENTS.md`, `.codex/**`, `.opencode/**`, untracked, so a fresh clone generates before it checks · generated from `CLAUDE.md` and `.claude/` by engine-sync
- Codex keeper: the one hand-written, tracked file under `.codex/` · `.codex/config.toml`
- marketplace: the plugin listing · `.claude-plugin/marketplace.json`
- plugins: the plugins professor tracks, each a git submodule of its own repo on `develop`, all but gpt-gateway listed in the marketplace; edits inside one leave this repo's status clean (`ignore = dirty`); an alpha `pfm install` installs every listed one as `{plugin}@professor` from a copy of its checkout in the pfm-owned `professor` directory marketplace `~/.local/share/professor-dev` (`claude.pluginCheckoutRoot` overriding), enabling those `claude.plugins` names · `plugins/{plugin}/`, `.gitmodules`
- CI: GitHub Actions — `ci.yml` tests every push to `develop` and every pull request into `main`; `release.yml` gates the release pull request, publishes its merge and verifies the published assets; `make release-check` runs the release checks locally · `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `Makefile`

## Path vars

- `$CDOCS`: `docs/commands`
- `$REFS`: `references`
- `$RESEARCH`: `research`
- `$RESOURCE`: `resource`

# Runtime

## Claude

- `CLAUDE.md` is the one hand-edited orientation file; `AGENTS.md` is compiled from it, never a symlink; each engine's adapter translates mechanics, never identity or protocol.
- Every `.claude/agents/*.md` role compiles for Codex and OpenCode.
- Saving a machine-global template is the deploy (symlink-live); on this host `~/.professor` is this checkout.
- The engine-sync plugin's Stop hook recompiles and checks both mirrors; after a Bash-driven write: `engine-sync codex build . && engine-sync codex check .` then `engine-sync opencode build . && engine-sync opencode doctor .`

## Codex

- Reads `AGENTS.md`, whose § Codex adapter comes from `.claude/engine-sync.json` (`codex.rootAdapter`), and `.codex/`.
- A role is a `.toml` twin `pfm install` (through `engine-sync global`, or `engine-sync codex agents`) writes as a marker-owned regular file (Codex refuses a symlink) into each Codex home's agent directory, so a role changes at install.

## OpenCode

- Reads the same `AGENTS.md` (preferred over `CLAUDE.md`) plus `.opencode/`: `agent/*.md`, flat `/name` commands, skill symlinks, and `opencode.jsonc` pinning the non-gitter Git-write denies.

## Local

- On the host, `.claude/scripts/dev.sh {status|install|build|typecheck} {templates|pfm}` only; `verify`, `test`, `cover`, `all` and `gate` refuse outside the fence.
- Scratch lives in `/tmp/{project}/{purpose}/`: `{project}` is this repo's directory name minus any leading dot (`.professor` → `professor`), derived, never hardcoded.
- One scratch subdirectory per purpose, owned by its protocol (`/tmp/{project}/{test|fence|lanes}/`); a run never dirties the checkout; a scratch path named to a human or a model is absolute.

## Fence

- `.claude/scripts/dev.sh iso {install|build|typecheck|verify|test|cover|all|status|e2e|shell} [project]`, `iso gate` or `iso {run|sim} {command…}` runs in the fence against the checkout it is invoked from (the main checkout or a `.worktrees/{flight}/` worktree), mounted read-only; it needs a reachable docker daemon.
- Narrowest test: `.claude/scripts/test.sh <path>[::<selector>]…`, the affected tests and static checks of the paths named; full gate, what done requires: `.claude/scripts/test.sh --full`.

## CI

- `.github/workflows/ci.yml` runs on every push to `develop` and every pull request into `main`: `macos-tests`, `fresh-install-linux` and `fresh-install-macos`.
- `.github/workflows/release.yml` runs on pull requests into `main` and manually (test artifacts only), never on a push: `check` (the mirrors and the HOL plugin scan), `build` and `release` gate the release PR; its merge's `publish` job tags `v{VERSION}` and creates the GitHub release, and `verify` checks the published assets against `SHA256SUMS`, the binary's version and the note.

## Host

- The host mirror build is `make -C pfm host-install`; `/pfm:update` sequences every installation here.

# On demand

- Before testing a change on `pfm/`: read `.claude/commands/pfm-testing-manual.md`.

# Rules

## Publication (this repo's sacred ground)

- **No push, tag or release without an explicit request in the current turn.** A finished task, a green build, a "finish it" or a completed release document is NEVER permission; the authorized Git writer publishes only on the user's plain ask in that turn.
- **`main` is release-only:** all work lands on `develop`; `main` moves only when `/pfm:release` merges the release PR from `release/v{X.Y.Z}` into `main` (gitter Phase RELEASE) — GitHub's ruleset refuses every other route; the merge tags the release, and no tag is pushed by hand · guard `.githooks/pre-push`
- **Nothing identifying ships:** no source-project brand, user PII, client domain content or machine-absolute path (`/home/…`, `/Users/…`) in any tracked file; write it clean the first time · guard `scripts/leak-check.sh`, run by `.githooks/pre-push`
- **Template example values are invented placeholders**, NEVER mined from a live private repo.
- **Version discipline:** `VERSION`, `CHANGELOG.md`, `releases/v{X.Y.Z}.md` and the tag MUST agree; between releases `develop`'s `VERSION` is the next `{X.Y.Z}-alpha`; `/pfm:release` owns the sequence.
- **Release notes are NEVER written during development:** `/pfm:release` derives them from `develop`'s diff against `main` and its commit messages.

## Dispatch

Tiers and effort live in the fleet prompt's § Model Selection; the cast and each agent's pinned model in `.claude/agents/` frontmatter.

- Every dispatch carries all five: the goal in one sentence and the artifact it returns, its shape named; the boundary, in scope and explicitly out; the anchors — exact files, symbols or commands, never "find the relevant code"; the tier and effort, plus a budget when the task can run away; what its own failure looks like — a dead end, an empty result, a tool that would not run.
- All sibling agents of a round go in one message; agents dispatched and reports received are counted and must match, and a missing report is a named coverage hole.
- An invariant enforced across layers (Go + shell + prompt) is mapped closed-world before the build dispatch; the brief carries every enumerated door — an invariant held at N−1 of its N doors is a violation at the missing door.
- Agent reports are evidence, not truth: verify a claim against what you can read yourself before relaying it.

## Prompt & template code

- A template is the live source file, verbatim — same structure, mechanics, character and logic; only project-specific values swap for placeholder tokens; the prose is never abstracted, skeletonized or genericized.
- One canonical token per concept — never a synonym for a registered placeholder.
- No dangling pointers: grep before you cite; a pointer to a file, agent or command the install does not produce is deleted, or its target ships.
- Every check names what its own broken state reports: a gate answering "fine" both healthy and broken is a coincidence detector.
- An error never renders as absence: "nothing there" and "we failed to look" read differently at the visible surface; logging alone is not sufficient.
- The judge is never the thing being judged: read the artifact from disk, never a verdict asserted in a brief; an empty enumeration is clean only once the enumerator provably ran, and the parent reports which.
- Surgical changes: every changed line traces to the task; fix broken things you hit; remove dead code, references and deps end to end, including `README.md`, `docs/SETUP.md` and `templates/refresh-map.json`.
- No duplication: grep for the existing rule, section or script and reference it; a near-copy drifts.
- Twins move together: a `.claude/**` change any adopter could use lands in its `templates/project/**` twin in the same pass, the commit message carrying the adopter-facing change; a customization only this repo wants stays local; unsure → ask.
- Right-size and finish: the simplest thing that works, no speculative abstractions, no stubs or deferred TODOs; where correct and convenient diverge, take correct, even at re-architecting cost.

## Engine code (Go / TS / JS / Python)

- Every `catch` and `if err != nil` logs full context and handles or returns the error.
- Validate at data entry.
- Follow the package's existing naming and structure; a new directory or pattern only when the task requires it.

## Process

- Commit only code whose tests pass.
- Code builds and tests run in the fence through `.claude/scripts/dev.sh iso`; the full gate is the lander's.
- Fence runs leave the host's `~/.local/bin` and the real `$HOME` untouched; a live config change the user orders is edited in place.
- Template, doc, prompt and config-file changes are small work, made in the main checkout.
- Once a fenced flight merges, the main chat runs the host mirror build under § Host.
- At every milestone, checkpoint the plan to a file, then compact before the next phase; a held turn arms an idle-fired self-call instead.
- AskUserQuestion is the user's whole screen: context travels inside the question text; each round simpler and more concrete, never a rephrase.

## Testing

- Every test runs through `.claude/scripts/test.sh`, which runs it in the fence; the affected tests run before claiming anything works, and only a suite you watched run is reported.
- The full gate runs once per worktree, right before its merge; a red one is fixed with `test.sh <failing paths>`, then the gate runs once more.
- A regression test counts only after it was watched failing against the unfixed code.
- A skipped or filtered suite is a named gap in the report, never a pass.
