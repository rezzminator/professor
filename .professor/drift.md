# Drift — this install's local customizations

Customizations of **this repo's own self-install** that must stay local and must NOT be
generalized into `templates/**`. `/pfm` appends here; `/pfm:release` never consumes it.

The test: would this make sense in a stranger's repo? If yes it belongs in `release.md` and its
template twin. If it only makes sense because this repo IS the blueprint, it belongs here.

## Update history

| Date | Version | Mode | Notes |
| --- | --- | --- | --- |
| 2026-08-13 | 0.53.0 | install (self-hosted, `minimal` profile) | Source is this working tree at `f07e6c0`, not a downloaded tag. 27 framework files written. Roster: `blueprint/`, `pfm/`, `dreamer/`, `ENGINES/wave-walker/engine/`. |

## Post-install customizations

- **KEEP-LOCAL: the self-hosted manifest and isolated fence are mechanically audited.** The manifest
  follows the live three-project roster, records the complete tracked Professor surface, and omits
  an impossible self-referential source SHA; `dev.sh test blueprint` verifies its version, roster,
  coverage, and hashes. Before Docker mounts the checkout read-only, the fence creates Walker's
  ignored nested volume targets and mounts the active Git common directory read-only, so Docker
  Desktop and linked worktrees run the same gates without weakening source immutability.

- **KEEP-LOCAL: the fence replaces "no worktree pipeline" (2026-08-20, user-ordered).** Code waves
  build in `.worktrees/{train}/` through `dev.sh iso` — the `infra/` pfm-dev container (fresh
  machine, worktree mounted; design `docs/dev/isolated-dev-foundation.md`). Markdown-only waves
  stay on `main`. The host mirror (build to `~/.local/bin/pfm` + `pfm install --yes`) runs only
  when a fenced wave fully closes: QA pass → orchestrator review, issues fixed → gitter merge.
  The adopter worktree pipeline (`worktree.sh`, `alloc-ports.sh`, per-project agents) remains
  uninstalled; this fence is the repo's own, container-backed variant.

- **KEEP-LOCAL: no `/pfm:update`.** This repo is upstream. There is no newer tag to replay a
  manifest against, so shipping the command would be a route to nowhere.

- **KEEP-LOCAL: `gitter` is trimmed to COMMIT / PUSH / PULL / TAG.** The SETUP / MERGE /
  WORKTREE-CHECKPOINT / SYNC phases and their `docs/commands/git/references/` cards are absent
  because the pipeline that dispatches them is absent. The Remote Publication Boundary, the banned
  commands, and the scoped-commit discipline are kept verbatim.

- **KEEP-LOCAL: the Codex execpolicy git lock is removed.** Gitter remains preferred; when it is
  unavailable, the active main Codex chat may perform scoped Git writes after explicit in-turn user
  authorization. Subagents stay read-only, and the publication boundary, leak gate, scoped-commit
  discipline, banned commands, and pre-push hook remain unchanged. The blueprint keeps its
  gitter-only prose law and commented-out lock because this fallback is specific to this repo.

- **KEEP-LOCAL: `.claude/skills/` is gitignored.** Source-fetched skills (`rr`) are cloned at
  install from their own public repos and never vendored — the `sources.json` law. Each also
  carries its upstream LICENSE naming its author, which this repo's leak gate correctly refuses.

- **KEEP-LOCAL: no markdown formatter hook.** `prettier` is absent on this host, and an
  `npx`-fetching PostToolUse hook is a silent network call in the middle of a turn.

- **KEEP-LOCAL: `blueprint` is a roster entry with mechanical gates instead of a build.**
  `dev.sh verify blueprint` runs the leak gate and the placeholder-registry gate. No other install
  has a project whose "tests" are a publication check.

- **KEEP-LOCAL: the `/wave:*` commands are installed here, rewired to this repo's cast.**
  `templates/commands/wave/{refine,live,walker,walker-invariants}.md` are the shipped source and
  keep the worktree pipeline; the installed copies under `.claude/commands/wave/` drop it, because
  this install lands every wave on `main`. The rewiring replaces an absent cast: `/jc` and its
  jc-core card become `dev` → `.claude/scripts/dev.sh` → `qa` → `gitter`; `/documenter` becomes an
  in-pass docs update routed through `/ptm` for guarded files; `Explore` readers become `tracer`;
  `/wave:orchestrator`, `/officer`, `/pm`, `/km`, `architect`, `db-admin-*` and `ui-ux-*` are cut
  entirely rather than left as dangling pointers, which removes refine's merge mode and live's lane
  mode (both orchestrator-invoked, so both had no caller here).

- **KEEP-LOCAL: `walker-invariants.md` § Engine Config carries a REPO-RELATIVE script path.**
  The blueprint template writes a machine-absolute path because an adopter's engine lives in a
  separate clone. This repo IS that clone and carries `engines/wave-walker/engine/dist/` in-tree, so
  the path is `engines/wave-walker/engine/dist/active-workflow.js`. An absolute `/home/...` path here
  would both pin one machine and fail `scripts/leak-check.sh`. For the same reason the
  templates address "the user", never a real name — the gate matches the name case-insensitively.

- **KEEP-LOCAL: the `args.project` profile carries no gate keys.** This repo exposes no
  request-authenticated surface, no roles, and no resolvers, so `authDoc`/`roles`/`gateResolverPattern`
  are absent and the engine reports `gates: SKIPPED — no project profile supplied`. That SKIPPED is
  the correct reading here, not a hidden pass; the thread walk carries every wave.

- **KEEP-LOCAL: `dev` and `qa` are global agents at the repo root.** One of each serves all four
  projects, parameterised by project name, rather than the blueprint's per-project `qa-{project}`
  roster — four projects do not justify eight agent files, and the per-project delta already lives
  in each project's own `CLAUDE.md`.

- **KEEP-LOCAL: `/wave:live` drops the `tmp/wave-boundary.lock` test mutex.** It guards a
  single-tenant shared test stack. This repo has none: `pfm`'s jail law gives every test its own
  `TMUX_TMPDIR`, and the npm suites are independent, so the lock would serialise runs that cannot
  collide.

- **KEEP-LOCAL: `/wave:ccc` installed, rewired to the on-main + fence pipeline.** The blueprint twin
  commands a worktree train (`/p:tokens` token probe, worktree-hygiene law, ONE WAVE = ONE WORKTREE
  = ONE MERGE) — none of which exist here. This install's rewire: ledger truth checks shas on
  `main` and names uncommitted done-work as a finding; hygiene checks stray dirs, a dirty `main`,
  and the fence worktree under `.worktrees/{train}/`; suite evidence reads `.claude/scripts/dev.sh
  test` logs, fenced code waves through `dev.sh iso` logs opening with their fence proof line;
  token burn reads seat statuslines from `chat_capture`; structure drift adds the gitter-only
  git-write law and the fence close order. The CCC identity itself (standing command seat replacing
  the one-shot sentinel) ships upstream via `release.md` — this entry records only the rewire.

- **KEEP-LOCAL: root `CLAUDE.md` rewritten lean — 137 → 118 lines, agent roster removed, `## Repo
  structure` added.** The cast table duplicated the harness registry (`.claude/agents/` descriptions
  are injected every session; `model:` frontmatter already pins each tier), so it died; § Subagent
  dispatch keeps only the briefing contract and dispatch laws, and `ptm.md`'s new-agent step
  retargeted off the dead table. The structure section now tells the truth the old file omitted:
  `agents/` (host-global `tracer`/`rr`, live copies at `~/.claude/agents/`, `.toml` Codex twins)
  and `harvester/` (Python/uv, own `CLAUDE.md`, outside `dev.sh`'s roster) exist. Meta's three-lenses
  bullet folded into § Cross-Disciplinary System Analysis (its canonical, cited home); Meta's
  remaining rules moved into Process. Every behavioral rule, threshold, and sacred-ground law
  survived verbatim in substance. Local file only — `templates/CLAUDE.md` is a different document
  and unchanged.

- **KEEP-LOCAL: `/p:tokens` installed from `templates/commands/p/tokens/` into `.claude/commands/p/tokens/`**
  (SKILL.md + README.md + token-ledger.mjs, 2026-08-21). Verbatim except the three Codex
  `PRICING` rows: `{CODEX_MODEL_FRONTIER}` → `gpt-5.6-sol`, `{CODEX_MODEL_SPEC}` →
  `gpt-5.6-luna`; the collector row is dropped because this host maps collector to the spec
  model (`build-codex.mjs` MODEL_MAP) and a duplicate substring row can never match. The
  `refresh-map.json` entries for these templates keep pointing at the source project's copies.

- **KEEP-LOCAL: `/p:rnd` + `/p:360` installed from `templates/commands/p/` into `.claude/commands/p/`**
  (2026-08-21). Value swaps only (sandbox path kept at `.professor/RND/{goal}` by the user's ruling); `/jc` · `/wave:builder` → `/wave:live`; `/km` + `knowledge/` → `/ptm` +
  `.claude/`; `{AI_PROJECT}` chain clause → the wave-walker engine's prompt modules + dist build;
  360's `{USER_NOUN} vs {SUBJECT_NOUN}` → `adopter vs maintainer`. 360 ships because rnd.md's
  blind-spot sweep points at `.claude/commands/p/360.md`.

- **KEEP-LOCAL: `/pfm` § Special Operations names `pfm codex build .` as the agent-TOML compiler.** This
  repo's Codex mirror has one writer, the Go `pfm codex build`; the repo-local `build-codex.mjs` is
  gone, so the "New agent" step and the scripts roster (a duplicated `codex-sync.sh`) now name the
  live compiler. `templates/project/commands/pfm.md` keeps `build-codex.mjs` — adopters ship the JS compiler.

- **KEEP-LOCAL: `scripts/placeholder-map.tsv` is untracked (leak-stop).** The scrub table necessarily
  holds this repo's real private values (name, email, domain, machine paths) mapped to placeholder
  tokens, and `scripts/leak-check.sh` excludes it from its own scan by design — so a tracked copy is
  a leak the gate can never see. It is now `.gitignore`d and read from disk locally, where the refresh
  pass still finds it. FLAGGED FOR THE MAINTAINER: this stops FUTURE exposure only; the value has been
  public in git history since `9b9663d` — purging the past needs a history rewrite + force-push, a
  destructive decision left to the user.

- **KEEP-LOCAL: the three memory-backup templates are `curated: true` in the public refresh map (leak-stop).**
  `blueprint/refresh-map.json` shipped their source paths verbatim: two under a private `~/work/<repo>/`
  checkout and one under the memory vault whose directory name IS this repo's `{MEMORY_VAULT_DIR}` value —
  the very thing that token exists to hide — all three naming private repos in a public file. The literal
  strings are deliberately NOT repeated here: this ledger is tracked and published too, and a note that
  quotes the paths it removed leaks them a second time. Recover them from the map's history if a hand
  refresh is ever needed (`git log -p -- blueprint/refresh-map.json`). No adopter can resolve those paths,
  so the honest public classification is `curated`. Refresh `templates/scripts/cc-memory-consolidate.sh`,
  `cc-memory-wire.sh`, and `memory-sync.sh` by hand from their host originals. TRADEOFF: `refresh-scope.sh`
  no longer raises a drift signal when the host copies change — this note is the only reminder.
  `settings-global.json → ~/.claude/settings.json` stays mapped: that path is generic to every install.

- **KEEP-LOCAL: `leak-check.sh`'s home-path rule is `~/work/[A-Za-z0-9]`, not bare `~/work`.**
  `$HOME/work/{MEMORY_VAULT_DIR}` and `~/work/<project>` are the blueprint's OWN documented defaults
  (`docs/references/memory-backup.md`, `templates/scripts/cc-memory-*.sh`) and must pass the gate; a
  concrete directory under `~/work/` names a private repo and must not. Same discriminator as the
  pre-existing `/Users/[A-Za-z0-9]` alternative — a home path leaks only when it names a real directory.
  This ledger is TRACKED: a drift note must describe a leak it fixed, never quote the string verbatim.

- **KEEP-LOCAL: `leak-check.sh --files` fails when it examined NOTHING (2026-08-26).** The files-mode
  loop guarded every path with `[[ -f "$f" ]]` and had no else branch, so a path that was not a regular
  file was skipped in silence and the run still printed `leak-check: clean`, exit 0. "We scanned forty
  files and found no leak" and "we scanned zero files" printed the same word — the coincidence detector
  this repo exists to hunt, sitting inside the publication gate itself. Found the honest way: the gate
  passed a file set that DID contain a leak, because the caller ran it from zsh where an unquoted
  `$changed` does not word-split, so all 69 paths arrived as ONE argument that matched no file. Now a
  non-regular path prints `NOT-SCANNED` by name on stderr, and a run that examined zero of the paths it
  was given fails outright. Absence alone does not fail — a deleted file in a changed-file list is
  ordinary and must not block a commit — but it is never counted as clean either. Red/green proven:
  `--files /nonexistent/xyz.md` was exit 0 "clean" before and is exit 1 after; a real file plus a
  deleted one still exits 0 while naming the unscanned path.

- **KEEP-LOCAL: `.claude/scripts/dev.sh` gate-honesty pass (2026-08-26).** This repo's build/test entry
  point, no blueprint twin (`templates/scripts/dev.sh` is the environment manager). Four fixes, each
  red/green-proven against the pre-change script: (1) bare `status` exited 0 while printing `WARN go —
  MISSING`, contradicting its own header contract — a missing `go`/`node`/`npm`/`git` now increments
  FAILURES; (2) `status {project}` ignored its TARGET arg and always reported the full fleet, so
  `status bogus` printed "all steps passed" — it now scopes to that project (new `proj_tools`: a tool
  the scope does not need warns, a tool it does need fails) and an unknown project exits 2 with usage;
  (3) `iso` checked only the `docker` binary, so a dead daemon surfaced as an opaque compose connect
  error AFTER `prepare-fence-mounts.sh` had already run its `mkdir -p` — `docker info` is now probed
  first and named TOOLCHAIN-MISSING; (4) the blueprint token gate demoted unregistered template tokens
  to `warn`, which nobody read and which let 27 tokens sit unruled — it now `fail_step`s, and the
  registry was completed upstream first so the flip lands green rather than permanently red.

- **KEEP-LOCAL: `scripts/leak-check.sh` reports its own broken state (2026-08-26).** The gate that
  guards this repo's publication; no blueprint twin. Files-mode swallowed read errors with `|| true`,
  so a permission-denied or unreadable file printed "leak-check: clean" and exited 0 — identical to a
  genuinely clean scan. A `grep` exit ≥ 2 is now a loud `SCAN-ERROR … treated as FAILURE, never as
  clean` line, red/green-proven against an unreadable file.

- **KEEP-LOCAL: roster and pointer corrections across this repo's own install (2026-08-26).**
  `CLAUDE.md` § Repo structure listed `agents/` as `(tracer, rr)` after `reviewer.md` joined it;
  `.claude/commands/ptm.md` still named the registered agents as "gitter and tracer" when
  `.claude/agents/` holds five, and now points at `ls` instead of a roster that re-rots;
  `.claude/agents/gitter.md` frontmatter claimed to be the ONLY agent allowed to run git writes,
  contradicting the § Process fallback that lets the active main Codex chat write when gitter is
  unavailable — it now reads "the registered Git writer — no other subagent runs git WRITES here";
  `.claude/commands/p/tokens/SKILL.md` pointed at `.claude/workflows/`, a directory this repo deleted,
  and now names the `wave-walker` engine; `.claude/commands/wave/live.md` claimed "this install has no
  worktree pipeline" while `CLAUDE.md` fences code waves — it now states what is actually true, that
  `/wave:live` lands on `main` under `/dev` verification and the isolated fence is for code-wave trains.

- **KEEP-LOCAL: published history rewritten to purge the scrub table (2026-08-26).** `scripts/placeholder-map.tsv`
  was tracked and published from `9b9663d` onward, carrying a real name, email, machine home paths, and
  private brand strings; `leak-check.sh` excludes that exact path from its own scan by design, so every
  pre-push gate reported clean while it sat in the tree. Two steps, both done: it was untracked
  (`git rm --cached`, commit `f96c660`) to stop future publication, then `git filter-repo` stripped it from
  all 357 published commits and the result was force-pushed over 5 branches and 77 tags.
  `origin/main` moved `f96c660` → `08b78b3`; 25 tags (`v0.44.0`–`v0.62.0`) were re-pointed. Verified from a
  FRESH clone, not from the mirror: zero commits and zero of 77 tags carry the file.
  Recovery handle for the pre-rewrite history: `~/professor-backup-f96c660.bundle` — 20,006,555 bytes,
  verified by CLONING it and confirming the clone resolves `f96c6608457f68ad9c2450429ec40963afb59b3f`,
  not by `git bundle verify` alone, which passes on a bundle that clones to nothing.
  **CORRECTION (2026-08-26), superseding the correction that stood here:** an earlier pass replaced this
  bundle with a local branch ref named `pre-rewrite-main-f96c660` and reported the bundle absent. Both
  halves are wrong, and each is re-checkable with one command: `ls -la ~/professor-backup-f96c660.bundle`
  returns the file, and `git show-ref --verify refs/heads/pre-rewrite-main-f96c660` returns
  `fatal: 'refs/heads/pre-rewrite-main-f96c660' - not a valid ref`. That ref did not exist when the
  entry claiming it was written.
  **Why that was not a documentation nit:** `git for-each-ref --contains f96c660` returned NOTHING — no
  branch, no tag, no remote ref reached the pre-rewrite tip. It survived on 3 reflog entries alone, so it
  was unreachable-and-collectable the day reflog expiry or any `git gc --prune` ran, and the ledger's only
  recovery pointer for 357 rewritten commits named a ref that was not there.
  **Both handles exist now.** This pass created the missing ref rather than merely correcting the sentence
  about it — `git branch pre-rewrite-main-f96c660 f96c6608457f68ad9c2450429ec40963afb59b3f`, local and
  never pushed — so the commit is reachable and gc-proof, with the bundle as the off-repo copy. A recovery
  record is the one document whose broken state must never read as healthy, and the cheapest way to fix a
  false claim is sometimes to make it true.
  **The rewrite was done against this repo's own `NEVER force-push` hard rule** (`.claude/commands/ptm/release.md`
  § Hard rules), on the user's explicit, thrice-reaffirmed in-turn override after being shown the full cost.
  The registered `gitter` agent refused the operation categorically and correctly — it also refused to treat a
  relayed quote of user consent as consent, which is the behavior a Git writer should have; the push was run
  from the main loop instead.
  **WHAT IT DID NOT ACHIEVE — do not record this as fully purged:** GitHub's `refs/pull/*/head` refs are
  read-only and rejected the push (`deny updating a hidden ref`, the reason the push exited 1). PR refs **#3,
  #5, and #7 still serve the file** and only GitHub Support can remove them — draft request at
  `tmp/github-support-purge-request.md`. One fork and any existing clone also retain the original history,
  permanently, by the user's accepted ruling ("that's not our responsibility"). The values are a name, an
  email, and paths — unlike a credential they cannot be rotated, so treat them as permanently disclosed and
  design accordingly.
  **Local fallout: closed.** No branch or worktree on this host carries pre-rewrite (`f96c660`) history —
  verified against both `git branch --list --contains f96c660` and `git worktree list`; the only ref that
  does is the deliberate recovery handle `pre-rewrite-main-f96c660`, kept unmerged on purpose. The cleanup
  that closed it is the next entry.

- **KEEP-LOCAL: all 21 stale worktrees purged (2026-08-26).** Ancestry was recomputed against the
  PRE-rewrite tip `f96c660`, not against today's `main` — the history rewrite changed every SHA, so a
  check against `main` reports `anc=NO` for all 21 and would have condemned the merged ones alongside the
  rest. Against `f96c660`: 18 fully merged, 3 carrying commits of their own — two `walker-consumer-tree`
  A/B benchmark checkpoints explicitly labelled "to be squashed by the builder" (the squashed result
  landed as `2abf196`) and `review/pr-5-combined`, whose subject landed as `b93b674`. All three were
  therefore superseded, and all 21 were removed. **Branch refs were NOT deleted**, so every commit
  remains reachable; only the checkouts are gone.
  Two things made the removal safe rather than merely convenient: three worktrees were DIRTY with work
  that exists nowhere else (`walker-v2` 12 modified + 108 deleted, `limits-clarity` 33 modified,
  `engine-contract-ready` 1), and one worktree was DETACHED with no branch ref, so its commits would have
  become unreachable and collectable. Everything was archived first to
  `~/professor-worktree-archive-20260826/` — per-worktree `git diff HEAD` patches, the untracked-file
  lists, and a `worktree-tips.bundle` — and the bundle was verified by CLONING it and confirming all
  three at-risk commits resolve, not by trusting `git bundle verify` alone.

- **KEEP-LOCAL: leak-check.sh stopped publishing its own denylist (2026-08-26).** The gate's `PATTERN`
  spelled out a surname, a first name and four private brand names in a tracked, public file — and
  line 65 excluded `scripts/leak-check.sh` from its own scan, so the one file guaranteed to contain every
  forbidden string was the one file the gate never read. It reported "clean" for months while being the
  leak. This is the repo's standing question landing on the instrument itself.
  The private terms now live in `scripts/leak-terms.txt` — untracked, gitignored, `LEAK_TERMS` env
  override, mirroring `genericize.sh`'s `GENERICIZE_MAP` idiom. **A missing or empty terms file is a hard
  non-zero failure**, never a pass: a fresh clone would otherwise scan for the structural patterns alone,
  find nothing, and print the same word it prints when the tree is genuinely clean. "We have no denylist"
  and "we found no leak" must not be the same output. Consequence to expect: `pre-push` and
  `dev.sh verify blueprint` now refuse to run on any clone without a terms file — that is the design, and
  the error message carries the fix.
  The self-exclusion is gone; the gate scans itself and passes. It passes by CONSTRUCTION, not by
  exemption: the three structural patterns are written as bracket classes (`/home/[A-Za-z0-9]`,
  `/Users/[A-Za-z0-9]`, `~/work/[A-Za-z0-9]`), and a bracket class cannot match its own source text.
  Only `scripts/leak-terms.txt` and `scripts/placeholder-map.tsv` remain excluded — the two files that
  hold real private values by design.
  Two further honesty fixes fell out of it. The empty-scan guard could not tell "excluded by design" from
  "named but not a regular file" and failed a list of only-excluded paths; it now counts the three
  outcomes separately and fails only when every non-excluded path went unexamined — which still catches
  the zsh one-argument trap it was written for. And widening the old machine-specific home pattern to `/home/[A-Za-z0-9]` (so
  another machine's path is caught too) surfaced 15 hits on invented fixture names; rather than exclude
  the test directories, an explicit `BENIGN_TOKENS` allowlist strips those exact tokens before judging,
  requires a non-word character after each so a longer real username is never suppressed, and REPORTS its
  suppression count on every run — an allowlist that hides its own work is the next coincidence detector.
  Three files were genericized rather than allowlisted, because they named real things:
  `docs/dev/trains/pfm-wave-2/waves/1-pfm-e2e-verification/walk.md` (a `~/work/<project>` reference whose
  own note had asked for exactly this scrub), `engines/deep-rr/engine/test/persist.test.ts`, and a prose
  false positive in `pfm-wave-2/STATE.md`.
  **Verified, not asserted:** eight red/green cases (missing terms, empty terms, comments-only terms,
  private term present, clean file, self-scan, benign token, near-miss username, mixed benign+real line,
  zsh one-argument trap); a full tracked-tree scan reporting `scanned 1274 file(s), 2 excluded, 0 leaks,
  93 benign suppressed`; and an end-to-end control planting a term in `README.md` and re-running the
  IDENTICAL command to prove the scan that says "clean" can actually fail. `mreza0100` was checked and
  is NOT a leak — it is this repo's own published GitHub identity, in `LICENSE`, `README.md` and
  `INSTALL.md` by design.

- **RESOLVED — phantom backup in an install log, root-caused and fixed (2026-08-26).** A `pfm install
  --yes` run on this host logged rewriting the user's global `~/.mcp.json` with "(backup preserved)",
  but no `.mcp.json.bak*` file existed anywhere under the home directory (`ls ~/.mcp.json*` showed only
  the live file). The installer's log message asserted a side effect that did not happen on disk.
  **Cause:** `internal/installer/mcp.go:135` passed a hardcoded `"rewrite <path> (backup preserved)"`
  into `installer.change`, while the `copyBackup` inside that same closure was guarded by `if existed`.
  Creating a file that had never been there therefore announced a preserved backup of nothing. Three
  further writers carried the identical shape.
  **Fixed** in `8173fae` (GitHub issue #9): a shared `changeDescription(path, existed)` now derives the
  wording from the SAME condition that gates the write. Pinned by
  `TestMCPInstallCreatesClientJSONWithoutClaimingABackup`, which runs the installer against a fresh temp
  home and asserts both the `create` wording and the absence of any `.bak` — the end-to-end reproduction
  of this exact report, watched failing against the unfixed code first.

- **KEEP-LOCAL: global agent installer now honors CLAUDE_CONFIG_DIR (2026-08-27).** `agents/build-global-agents.py`
  hardcoded `~/.claude/agents` and `~/.codex/agents` as its install targets. This host runs Claude with a
  non-default `CLAUDE_CONFIG_DIR`, so every `.md` role installed "successfully" into a directory no
  session reads: `rr` and `reviewer` were absent from the registry and chats reported them as not found,
  while `tracer` resolved only because someone had hand-symlinked it into the real config dir. The Codex
  half was never affected — `~/.codex/agents/` had all three `.toml` twins, byte-identical to source.
  The installer now reads `CLAUDE_CONFIG_DIR` / `CODEX_HOME` with the old paths as fallbacks. Re-run
  installs `rr`, `reviewer`, `tracer` into `$CLAUDE_CONFIG_DIR/agents/` as real files (its `install()` already
  unlinks a symlink first, by design — the registry must not depend on the repo directory surviving).
  **The failure mode is the interesting part:** an installer that writes to the wrong directory reports the
  same success as one that writes to the right directory. It printed `installed …` for years' worth of runs
  and was never wrong about anything except where. Nothing downstream verified that the file it wrote was
  in a directory the runtime actually reads — a write is not an install.
  Registries load at SESSION START, so a re-run never fixes the running session; the roles appear in the
  next one.

- **KEEP-LOCAL: `docs/commands/pfm/references/refresh.md` § Output structure drops hardcoded rosters.**
  The tree diagram named dead commands (`animate`, `slow-burn`) and pre-split paths; it now shows the
  two-scope shape only and points at the tree + `refresh-map.json` for rosters (2026-08-29). The card
  documents this repo's own refresh pass and never ships.

- **KEEP-LOCAL: host statusline + tmux-title overlays are `pfm install`-owned**
  (2026-08-29, user-ordered; installer ownership 2026-09-04, issue #14 F1).
  `pfm/internal/installer/assets/bin/pfm-statusline` — context-gauge overlay over `pfm statusline`
  (recomputes true occupancy from the transcript; meltdown ladder; `1h∞`); its gauge regex walks glyph
  runs interleaved with ANSI escapes — `makeBar` emits color+filled+dim+empty, so an escape sits
  MID-BAR at any nonzero percent, and the old single-run pattern matched only the empty half (the
  double-bar defect). `pfm/internal/installer/assets/bin/tmux-title-renudge` — OSC-title re-emitter
  the systemd trio (`~/.config/systemd/user/tmux-title-renudge.{path,timer,service}`, hand-rolled,
  un-tracked) fires at `%h/.local/bin/tmux-title-renudge`. `pfm install` materialises both into
  `~/.local/share/pfm/install/bin/` (0755) and symlinks `~/.local/bin/pfm-statusline` /
  `~/.local/bin/tmux-title-renudge` to the managed copies — idempotent, unwired on uninstall;
  `settings.go` points an empty, legacy, or bare `pfm statusline` `statusLine.command` at the
  overlay and preserves any other custom command; `pfm doctor` FAILS when a symlink is missing or
  displaced or a `statusLine.command` still names the raw `pfm statusline`. Neither ships to
  adopters — a per-host overlay, not a template asset.

- 2026-08-29: this host runs `claude.systemPrompt = "professor"` (~/.config/pfm/pfm.config.json); the RND
  sandbox `.professor/RND/harness-prompt/1-load-bearing/` keeps the capture/drift tooling (sink.py,
  snapshot-harness-prompt.sh, check-harness-drift.sh) and the v2.1.251 baselines pfm's doctor check was
  built from. A bare `claude` typed inside an existing tmux pane still takes the launcher's TMUX
  passthrough (pre-existing) and gets the production prompt; fleet spawns inject.
- 2026-08-29: `scripts/pfm-statusline` gauge learns the post-compact window — a `compact_boundary`
  entry newer than the last usage entry means that usage is the pre-compact corpse; render instead
  `compactMetadata.postTokens` + the project's cached system floor, marked `~` (estimate). Missing
  both → the pre-compact number stands (stale beats blank). Verified against a live post-compact
  transcript: estimate ~77.0K vs actual 76.1K first call.
- 2026-09-02: **KEEP-LOCAL: root `CLAUDE.md` gains § How the framework reaches an adopter, the
  twins-move-together rule, the map-before-dispatch law (folded from retro 2026-08-29), and ledger
  consumers on the `.professor/` line.** This file describes the blueprint's own machinery from the
  upstream side; `templates/project/CLAUDE.md` is a different document and unchanged.
- KEEP-LOCAL: Codex project default (2026-09-05) — removed the repo-local model pin so trusted
  sessions inherit the user-level default; retained this repo's `xhigh` reasoning-effort override and
  re-stamped its self-hosted manifest entry. (cost)

- **KEEP-LOCAL: `/pfm:refresh` — the blueprint re-derivation command** (`.claude/commands/pfm/refresh.md`). Maintainer
  machinery, the sibling of `/pfm:release`: it re-derives `templates/**` from a live source project and ships no
  adopter twin, because an adopter consumes the blueprint and never derives it. It owns the execution mechanic that
  `$CDOCS/pfm/$REFS/refresh.md` (the law) never carried: the pass is diff-driven rather than file-driven — the
  orchestrator reads no template and no live source, a worker reads at most its 2 files, one worker owns one template
  pair — and every diff hunk is classified `SYNC` (framework change, applied) / `LOCAL` (project-specific, never
  applied) / `TOKEN` (value the template parameterizes) / `UNRULED` (reported, never silently dropped), applied in
  reviewed `sonnet`-effort-High batches ordered smallest-diff-first with a review gate between waves. `/pfm:release`
  step 3 was rewritten to DELEGATE to it rather than restate the mechanic — the duplication that would have drifted.
  Its `rulings` mode settles `MISSING-SOURCE` / `UNMAPPED-LIVE` map entries and carries the two integrity checks the
  scan structurally cannot make, because `refresh-scope.sh` reads the map's keys rather than the template tree:
  ZOMBIE (map entry whose template file does not ship) and ORPHAN (shipped template with no map entry).

- **KEEP-LOCAL: `templates/refresh-map.json` re-ruled against the live source's re-layout (40 rulings).** The scan was
  BLOCKED at 41 `MISSING-SOURCE` + 20 `UNMAPPED-LIVE`; it now reports 6 and 0. CURATED (7): the six machine-global
  templates still carrying a dead live source (`global/agents/scheduler.md`, `global/commands/git.md`,
  `quality/{doc,prompt}.md`, `wave/{ccc,refine}.md`) — `templates/global/**` IS the truth by law, so a global entry
  with a live source was a mapping bug — plus `project/scripts/build-codex.mjs`, retired live-side and now blueprint-
  only. RETARGET (3): the ZOMBIE keys `global/commands/wave/{live,orchestrator,walker}.md`, whose template files had
  already moved to `project/commands/wave/` under the wave scope split while the map still named the global paths.
  REMAP (28): the 18 `{role}-{project}` → `{project}-{role}` agent renames across `role-wrapper.md` + `qa-wrapper.md`,
  the two `per-project/{developer,qa}.md` entries whose backend sub-project `.claude/` was consolidated into the flat
  root registry, and the 8 reference docs the live project moved from `docs/commands/{cmd}/references/` to a flat
  `docs/references/` (pfm's renamed `pfm-audit-scopes.md`). `source_globs` follows that move; `.claude/scripts/prod-
  deploy-watch.sh` joins `ignore_sources` beside its already-ignored `prod-deploy.md` sibling. The 6 survivors are all
  DELETE rulings whose cascades are end-to-end removals, not map edits, and are queued for the batch pass:
  `commands/p/360.md` (the user's ruling, 360 removed end to end — it has ~10 citing files), `scripts/checkpoint.sh`,
  `codex/skills/wave-builder/SKILL.md`, and the three reference docs with no successor (`gitter-history.md`,
  `debug-discipline.md`, `build-reference.md`).

  (2026-09-15): `role-wrapper.md` and `qa-wrapper.md` — the two agent templates this REMAP renamed keys for — are
  removed from the shipped templates entirely, along with `mono-architect.md`, `mono-documenter.md`, `mono-
  planner.md`, `rndier.md`, `km.md`, `pm.md`, `documenter/archive.md`, `audit/ai-output.md`, and `km-guard.sh`. Their
  `templates/refresh-map.json` entries are dropped in the same pass.

- Local: `.codex/skills/deep-rr` untracked (`git rm --cached`) and ignored in `.gitignore` beside the `.claude/skills/` rule it mirrors. It was a tracked symlink into `.claude/skills/deep-rr`, a tree `.gitignore` has always ignored by design (source-fetched skills are never vendored — each carries its own upstream LICENSE the leak gate refuses), so the link resolved in this working copy and in no clone. `compileRepoSkills` (`pfm/internal/codexgen/compiler.go`) recreates it from whatever `.claude/skills/` holds on every `pfm codex build`, so nothing needs it in the index; the repo's own `.gitignore` comment already stated the law — "skill symlinks stay untracked like `.codex/skills/`" — while the index contradicted it. It had broken three gates in three different voices; the new tracked-symlink reconcile in `check-codex-markers.mjs` (release queue) names the next one on sight. Verified: the gate reported `UNTRACKED-TARGET .codex/skills/deep-rr` before the untrack and `1 tracked symlink(s) resolve to tracked targets` after — the survivor being `engines/wave-walker/engine/dist/active-workflow.js -> workflow.js`, whose target IS tracked.

- **KEEP-LOCAL: output styles are retired, and their absence is asserted rather than assumed (user-ordered).**
  No output-style file, directory, or `outputStyle` settings key exists anywhere in the tree, and every Claude launch
  now pins `--settings {"outputStyle":"default"}`, so a style file that reappeared would be INERT — nothing would
  fail and no persona would change, leaving a file everyone believes is doing something. The four surviving dead
  references are gone: `build-opencode.mjs` no longer claims a "persona-adoption pointers stripped" transform it
  never implemented (grep-verified: the comment was the only occurrence, there was no code) nor lists output-styles
  among what it does not cover; `check-self-hosted-manifest.sh` drops the `output_styles` category whose want-list
  and got-list were both permanently empty — a comparison that printed the same word healthy or broken — and gains
  an absence gate in its place (tracked `.claude/output-styles/`, the directory on disk, the manifest key, and an
  `outputStyle` key in any of the three settings files each fail by name); `docs/commands/pfm/references/refresh.md`
  retargets the Analysis Protocol row from the vanished "Professor persona output style" to the fleet prompt
  (`templates/prompts/professor.md`), matching `docs/BLUEPRINT.md`'s canonical statement; the manifest drops
  `installed.output_styles`. Verified by negative test: the gate names the directory, and names the manifest key,
  and goes quiet again once each is removed. `templates/**` carried no output-style reference to begin with, so
  nothing ships — hence local.

- **KEEP-LOCAL: `build-opencode.mjs` reports a dangling command source instead of dying on it.** A retired global
  command leaves a symlink in `$HOME/.claude/commands/` whose blueprint target is deleted (`/rnd` after its
  global -> project move); `readFileSync` threw ENOENT out of `compileCommands` and killed the whole compile, so ONE
  stale link cost every other command its output and the failure read as a crash rather than as the single missing
  source it was. A dangling source is now collected and always stated: `generate` names it and finishes, `check` and
  `doctor` refuse. Verified both ways on the live `/rnd` links — `generate` completed 28 outputs with a `note:` line,
  `doctor` returned DOCTOR FAIL naming the source. Repo-local: `templates/project/scripts/` ships `build-codex.mjs`,
  never this compiler.

- **KEEP-LOCAL: `develop` is the integration branch; `main` is release-only (user-ordered).** GitHub
  rulesets `main-release-only` (pull request + the four required checks, no force-push, no deletion,
  no bypass actors) and `develop-linear-history` (no force-push, no deletion) enforce it remotely;
  `.githooks/pre-push` refuses `refs/heads/main` locally with the road to take. `/pfm:release` commits
  and pushes on `develop`, then gitter Phase RELEASE opens the `develop → main` PR, waits for the
  checks, merges, tags the `main` commit, pushes the tag, and fast-forwards `develop` back onto
  `main`. CI (`verify.yml`, `install-verify.yml`) runs on `develop` pushes too. Rewired: root
  `CLAUDE.md` § Publication/§ Process, `.claude/agents/gitter.md`, `.claude/commands/pfm/release.md`,
  `.claude/commands/wave/live.md`, `docs/dev/isolated-dev-foundation.md`. The `templates/project/**`
  twins keep the adopter's single-branch pipeline — this flow exists only because this repo IS the
  published blueprint.

- **KEEP-LOCAL: gitter drops a harness `Claude-Session:` attribution URL from every commit message.**
  The Claude harness's attribution reminder asks for a session URL trailer; this repo publishes
  every branch, so gitter keeps the `Co-Authored-By` line and drops the URL. An adopter's private
  repo may keep the harness default — the template twin stays silent on it.

- **KEEP-LOCAL: `dev.sh verify pfm` runs pfm's architecture ratchet; `pfm/CLAUDE.md` points at what exists.**
  `verify pfm` runs `pfm/scripts/arch-check.sh` (C1–C16 against `pfm/.arch/`) after `go vet`, and the
  ratchet reads the fence's mounted git dir, so the gate holds inside `dev.sh iso`. `pfm/CLAUDE.md`
  drops its dangling `PLAN.md`/`CUTOVER.md`/`check/`/`legacy/`/`PFM_DB_SCRIPT` pointers, replaces the
  package table with the `go list` doc map, and names the façades and the ratchet. pfm is this repo's
  own engine, so the `templates/project/scripts/dev.sh` twin carries no ratchet.
- Project: `pfm/CLAUDE.md` + `pfm/scripts/arch-check.sh` — the ratchet runs in the C locale (the
  baselines are byte-ordered), C3/C4 report ERROR when no `cmd/pfm` source is listed, C12 counts a
  `PFM_*` name only when production code uses it beyond declaring it, and C16 also catches env reads
  through a `"PFM_*"` constant. The dead `PFM_CODEX_AVAILABLE` knob is gone from the doc and the tree;
  K3 names `internal/chat` as the typed verbs, with the remaining MCP argv verbs counted by C10.
- Local: `scripts/description-check.sh` + its `dev.sh verify templates` gate — parses every tracked markdown frontmatter and ledgers the description budget (heaviest first, count over the 400-char tier). It exists because no other instrument sees the failure: `/quality:description` states the law but a prompt rule cannot detect an unquoted `: ` that breaks the YAML, Claude Code registers the entry anyway, and `rumdl check` ignores frontmatter entirely. Exit 2 TOOLCHAIN-MISSING (python3/PyYAML absent — nothing was parsed), exit 3 NOTHING SCANNED (the scan is broken, not the tree), exit 1 a real parse failure; all three watched. Repo-level gate like `leak-check.sh`, not shipped to adopters — the law and the policy are what reach them.
- Local: `.claude/settings.json` wires the `format-md.sh` PostToolUse hook, which this install had deliberately left unwired while it called `npx prettier` (recorded in `manifest.json`: prettier absent on this host and an npx-fetching hook is a silent network call mid-turn). With `rumdl` provisioned by `pfm install` the objection is gone. `.gitignore` gains `.rumdl_cache/`.
- Local: `/markdown` was promoted out of this install's drift — the generic half became the machine-global `/quality:md-forlint` (see `release.md`), and the source project keeps only its own deltas: which live prompt trees route to `/km`, and its private RND evidence paths.

- Local: the source project keeps its own formatter automation — its `format-md.sh` hook stays on `npx prettier` (on the user's order: "just don't automate it there"), and its `.rumdl.toml` disables MD060 so a manual `rumdl fmt` never compacts a table prettier will re-pad on the next edit. The config and the `/quality:md-forlint` route are available there; nothing there runs rumdl unasked. Seven flat `.claude/skills/*.md` files removed there — the harness only discovers `skills/<name>/SKILL.md`, so they had never loaded.

- Local: the source project's `/jc` removal keeps the fix PROCEDURE under a neutral name, because its `gitter` has no generic COMMIT phase — only DOCS-COMMIT and JC-COMMIT — so deleting the phase would leave `/wave:live` and `/contentor` unable to commit. There: the command, its persona overlay (`.claude/prompts/jc.md`) and the Codex mirror are deleted, `docs/references/jc-core.md` becomes `fix-core.md` with its identity stripped, the phase is renamed COMMIT, the documenter mode FIX-UPDATE, and every route now names `/wave:live`. The blueprint's twin takes the SAME rename, not a deletion: its `gitter` carried only DOCS-COMMIT and JC-COMMIT too, so deleting the phase left `/wave:live` W5 with no commit mechanism at all — the brief that said otherwise was wrong about the file. Its documenter did already carry ARCHIVE. The source project's residue is cleaned in the same pass: `/dev`'s autoheal prose, the documenter mode label, the walker's WALK field and commit-SHA lines, the fix-core card's own leftovers, `docs/references/pfm-refresh.md`'s cast lists, and the access-control audit workflow's finding schema.

- Local: `/pfm:release` runs from two release worktrees — `.worktrees/release/main` (detached, byte-identical to `origin/main`) and `.worktrees/release/develop` (branch `release/v{NEW}`) — so the live checkout's WIP is never swept. Sonnet reviewers read `origin/main...HEAD` per area and return defects plus changelog bullets for un-ledgered changes; fixes and the release commit land on the candidate branch; both sides gate in the fence; then a Codex model on medium effort installs the stable release on a fenced adopter machine and updates it to the candidate exactly as the docs say, with every FRICTION fixed, reverted to the stable snapshot and re-run until CLEAN (`docs/commands/pfm/references/release-rehearsal.md`). `infra/release-rehearsal.sh` owns that machine: `up`, `seed` (a rehearsal-local upstream from this repo's git objects), `publish` (the candidate tagged inside the container only), `snapshot`/`revert`, `exec`, `status`, `down`. Pre-flight now refuses to publish when `main` lacks any `main-release-only` ruleset rule. Repo-only: the command publishes this blueprint, so no template twin.

- Local: `.claude/scripts/dev.sh iso` hands the untracked leak denylist into the fence read-only (`LEAK_TERMS`, else the main checkout's `scripts/leak-terms.txt`), so `iso all templates` from a linked worktree runs the real leak gate instead of failing on a terms file the worktree mount can never carry; with no denylist anywhere it still fails loudly. `infra/release-rehearsal.sh` snapshots the stable adopter machine as an archive of its HOME in a volume (`pfm install` writes nothing outside HOME) rather than a committed image, which Docker Desktop's content store could not produce once a concurrent `iso --build` replaced the base image. The adopter `dev.sh` carries no `iso`, so no template twin.
- Local: `/pfm:release` + root `CLAUDE.md` § Version discipline + `docs/RELEASE.md` — `develop` carries the next development version `{X.Y+1.0}-alpha` between releases: Step 1 computes `{NEW}` from the newest tag, Step 8 drops the suffix, Step 13 opens the next `-alpha` line on `develop` (VERSION, `.professor/VERSION`, manifest `installed_from.version`) and pushes it. Tags never carry a suffix; pfm's tag parsers keep rejecting one, and `updatecheck` ranks a pre-release below its release.
- Local: `scripts/description-check.sh` reaches git inside the dev fence through the `PFM_DEV_REPO_GIT_DIR` / `PFM_DEV_REPO_WORK_TREE` pair (the `leak-check.sh` route — a worktree's `.git` file names a host path the container cannot see) and exits 4 `SCAN-BROKEN` when git cannot locate or list the repository; `.claude/scripts/dev.sh` maps exits 1/2/3/4 to distinct lines and any other exit to "crashed — NOT a verdict on the tree". The fence used to report a git failure (exit 128) as "a tracked frontmatter does not parse as YAML".
- Local: `/readme-gif` re-records the README hero GIF (`docs/img/pfm-fleet.gif`) through `infra/readme-gif/record.sh`: the dev fence container on this checkout, pfm built with the release stamp and `pfm install`ed normally, a fake `claude` that keeps the harness contract pfm reads (argv[0], a transcript, the statusline run that writes the live crumb, `-p` print mode answering the Limits credential refresh), mock usage records for two Claude seats and two Codex homes (placeholder Codex sign-ins pfm's config validation requires, never spent), an invented fleet with signed messages, the unsigned-spawn ledger rows removed so the cosmos draws without a warning, Starship on frosted glass, VHS frame layers composited into Pillow-drawn window chrome, and a two-stage ffv1 → bayer-palette encode. The command reads four stills before reporting. Repo-only: it records this blueprint's README, so no template twin.
- Local: `scripts/leak-check.sh` reads a machine-private ignore list from its own terms file: `!ignore-token <lowercase regex>` strips a known-public string (the public GitHub handle, a code identifier a private term happens to match inside) from a line before it is judged, so a real leak on the same line still fails, and every suppression is counted in the verdict; `!ignore-path <git glob>` skips a path and names it on every run; an unknown `!` directive fails the run instead of becoming a term. Six controls watched under macOS bash 3.2 (the pre-push shell): handle alone passes, handle plus a real email fails, the code identifier passes, the real value that term targets fails, an ignored path is announced, a bogus directive fails. The ignore entries live in the untracked terms file, synced by hand like the terms.
- Local: the repository's GitHub owner is `rezzminator` (renamed from `mreza0100`, which GitHub redirects): `/pfm:release` Constants and Pre-flight name `rezzminator/professor`, `.professor/manifest.json` records it as `installed_from.repo`, and `scripts/leak-check.sh` keeps `mreza0100` as a benign token only because historical release notes still quote it.
- Local: `.claude/scripts/dev.sh verify pfm` runs CI's gofmt check between `go vet` and the architecture ratchet (`gofmt_clean`: exit 1 names each unformatted file, exit 2 says gofmt could not run and nothing was checked). gofmt's output differs across Go releases, and a host Go newer than `go.mod`'s pin called `pfm/internal/ask/ask_test.go` clean three times while CI's pinned Go refused it; through `iso` the fence gives CI's verdict. The adopter `dev.sh` has no pinned-toolchain fence, so no template twin.
- Local: `.claude/scripts/build-opencode.mjs` mirrors the root `LICENSE` and `SECURITY.md` as byte copies into `.opencode/` (claimable by name, no marker; `check` reports them STALE/MISSING; a missing root source is a note); `scripts/leak-check.sh` excludes the LICENSE copy exactly as it excludes the root file. The HOL AI Plugin Scanner scores `.opencode/` as a package of its own and reads LICENSE without following symlinks, so the mirror lost 6 points for files one directory up. Repo-only: the compiler ships to no adopter, so no template twin.
- Local (2026-09-15): the adopter's framework change manager ships as `/pcm` (`templates/project/commands/pcm.md`,
  audit scopes inline) and `/pfm` ships as the CLI guide; the machine-global commands (`quality/prompt`,
  `context-meter`, `md-forlint`, `wave/ccc`) now point at `/pcm`. THIS repo's own manager is still
  `.claude/commands/pcm.md` too (renamed the same day); `/pfm:release` stays this repo's local publish
  subcommand under `docs/commands/pfm/references/`, and `docs/commands/pcm/references/` carries the manager's cards.
- Local (2026-09-15): `.claude/agents/{tracer,scheduler}.md` and `.claude/commands/{quality,wave}/*.md` are rewired
  local variants of their machine-global originals (this repo's anchors: `templates/**` in trace scope, placeholder
  hops, the dev/qa/gitter cast) — kept as files, not symlinks; root `CLAUDE.md` § Repo structure names the exception.
