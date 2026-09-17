# Wave spec — reload feedback, unit-test law, realistic DFS integration suite, infra/fence

**Status:** QUEUED for GOD on the devbox (Fable, xhigh). Written on the Mac at the close of the reliability train; the user is present and token-rich for the next ~15 h. Read whole before acting.

## Context (state when this was written)

- `develop` carries the reliability train (gates, fmt, lint 0, dream retired, layout moves, extractions, e2e harness, tier B verifier, reviewer findings, tier B defects P10–P12). Fence green: `dev.sh iso test pfm` (go test + tagged e2e), `iso verify pfm` (vet, fmt-check, lint-new, arch C1–C21), `iso verify templates` (clone ratchet 25, leak-check, manifest).
- Tier B (`infra/demo/up.sh --fresh` + `verify.sh`) on a fresh container: 12 ✓ / 2 ✗; the two reds are host-side — harvestpy on linux-arm64 (`torch` pins `nvidia-cusparselt-cu13`, its arm64 wheel fails `uv pip check`; needs a networked relock) and a seat whose OAuth token expired on the host.
- Not host-installed anywhere yet; the devbox decides its own `make host-install` + `pfm install --yes`.
- Backlog carried over: harvestpy arm64 relock; OpenCode compile layer for adopters (`build-opencode.mjs` is not shipped by `pfm init`); `pfm chat_open` must refuse a live OpenCode session; `chat_self_compact` then-steer never fires on an OpenCode seat; go-test-coverage parses thresholds as int; `wireSettings` implicit-seat gap; `cmd/pfm` jail-teardown flake under load; per-file coverage floor; 36 tests ≥ 2 s; `inject.IsBusy` does not match "Waiting for N background agent" (named non-change).

## Task 0 — `/reload` feedback (root cause diagnosed, fix pending)

Diagnosis (verified on the Mac): `/reload` WORKS — the pane was respawned onto the requested seat — but its feedback lies twice.

1. `internal/hookentry/prompt_block.go` answers a successful `/reload` with `{"decision":"block","reason":"","suppressOriginalPrompt":true}`. Claude Code 2.1.268 renders that as `UserPromptSubmit operation blocked by hook: Blocked by hook · Original prompt: /reload …` — the same word the real failure path uses.
2. A `/reload` typed mid-turn makes the worker hold `/exit` until the turn ends (`internal/reload/reload.go` `waitCallerIdle`, 120 polls), announced only in `/tmp/cc-sid/reload-<socket>.log`. The screen shows nothing for the whole wait.

Fix: the quiet block carries a truthful reason (`reload scheduled — reboots when this turn ends (log <path>)`); the worker announces the hold on the pane itself (`tmux display-message`, already used for refusals) and again when it types `/exit`. Red-first: the intercept's JSON reason, and a fake-tmux test asserting the hold notice. Twin check: the Codex path (no UserPromptSubmit hook) is unchanged.

## Task 1 — Unit-test law (host, mocked, concurrent, edge cases)

The user's contract for every unit test in `pfm/`:

- A unit under test has its dependencies on the host, the account, the API and every external process **mocked** — the test runs on the host (`make test`), never needs a seat, a token, tmux, systemd/launchd, Docker, or the network. Existing fakes (`reloadCommandTmux`-style interfaces, `testjail`, fixture transcripts) are the pattern; where a package still shells out or reads the real home, introduce the interface at the seam and log it in the package's `doc.go`.
- **Concurrent**: `t.Parallel()` on every test that can take it; package-level `TestMain` jails stay; the target is the 36 slow tests (`tmp/logs/P2.1-slow-tests.txt` on the Mac — re-measure on the devbox with `go test -json` and rank).
- **The ten host edge cases**, each a named shared fixture reused across packages, each with at least one test where it matters (map first with tracer, then dispatch by package):
  1. `HOME` unset or unwritable (read-only home, `EROFS`).
  2. Config dir is a symlink (physical vs logical paths — the class of the P10.1 defect).
  3. Case-insensitive filesystem (darwin default) vs case-sensitive (linux).
  4. `tmux` absent, or older than the minimum version.
  5. No service manager (no systemd on linux, no launchd on linux, container without either).
  6. `TERM`/locale missing or non-UTF-8 (statusline, cosmos glyphs, title renudge).
  7. Paths with spaces and non-ASCII characters in HOME, cwd, and worktree.
  8. Expired or absent credentials (OAuth expired, Keychain entry missing, `auth.json` absent) — every probe must report NOT-LOGGED-IN, never "no chats".
  9. Stale artifacts: dead tmux sockets, pid files of exited processes, a `fleet.db` WAL from a crashed writer, a reload lock left behind.
  10. Two writers at once: concurrent `pfm install`/`pfm chat new` against the same ledger, settings.json, or manifest (atomic writes and locks must hold).

Deliverable: a `pfm/TESTPLAN.md` section naming the ten fixtures and where each lives; `make test` on the devbox host green and faster than the fenced baseline; every skipped/filtered test named.

## Task 2 — Realistic DFS integration suite

Goal: prove the WHOLE of pfm + Professor end to end, as a user experiences it, from one starting point, diverging lane by lane, depth-first.

Root (shared, once per run): fence up → `pfm install --yes` → Professor installed on a real project (the express clone via the existing adopt flow) → `pfm` launched: an empty fleet.

Lanes off the root, depth-first, each lane fully exhausted before the next starts:

1. **Engine lanes**, in order Claude → Codex → OpenCode. For each: open one chat; assert labels, statusline, tmux title, theme, fullscreen; then the `/reload` flag matrix (`--account`, `--model`, `--effort`, `--1h on|off`, `--new [--hide]`, `--then`, `--sock`) — every combination pfm supports, each confirmed by what the respawned pane actually shows; then close the lane.
2. **Fleet lane**: create chats of every kind and label shape pfm supports (`{name}`, `{name}:{group}`, every engine, headless, hidden); then the TUI tabs in order: stats (every chat where expected), limits, cosmos.
3. **MCP lane**: prove the install wired every MCP into every engine (registry files, ownership ledger, doctor), then poke every tool of every MCP from a chat (chat_*, harvester_*) and assert each answer shape — an error must render as an error, never as absence.
4. **Adopter lane**: the express install itself — `pfm update check` clean, Codex mirror check, hooks rooted and present, guard hook denies, `/dev` gate runs the project's tests.
5. **Ops lane**: inject round trip, self-compact, storm + kill-storm, idle down/up, headless, name-sync, `pfm doctor` clean (or every warning named), uninstall leaves nothing owned behind.

Design rules: enumerate the functionality inventory FIRST (`pfm --help` tree, `docs/demo/inventory-*.md`, the installer's asset roster, the MCP tool lists) and map every item to exactly one lane — an item mapped nowhere is a named hole. Parallelism: lanes that share no mutable state (separate containers or separate seats/fleets) may run in parallel; GOD designs the lane graph from their overlap and reports the achieved parallelism. Reuse `infra/demo/verify.sh`'s beat shape (one ✓/✗ line per check, failure text quoted, artifacts under a named log). The suite is the wave-close and release gate; tier A (`pfm/e2e`) stays the hermetic fast lane.

## Task 3 — `infra/` restructure

- Create `infra/fence/` and move everything that builds or drives the isolated fence there: the compose file, the `pfm-dev` image, `fence-env.sh`, `tools.env` + `tools.sh`, `release-rehearsal.sh`, the manifest check if it is fence-only. `infra/demo/` keeps ONLY demo content (the deck, the demo scripts that use the fence), never fence setup.
- Remove `infra/readme-gif` entirely (the mocked recorder) — dead end to end: pointers in `README.md`, `docs/`, `refresh-map.json`, manifest roster, `.github/workflows`. Open question for the user before touching it: `infra/readme-cards` (the newer README recorder) — keep or remove.
- Every consumer re-pointed: `.claude/scripts/dev.sh iso` (routes through `/pcm`), `infra/demo/up.sh`, `docs/dev/isolated-dev-foundation.md`, root `CLAUDE.md` § Repo structure (via `/pcm`), `infra/check-self-hosted-manifest.sh` roster, CI. Gate: `iso verify templates` (manifest, leak-check) and a fresh `iso status` from a clean clone.

## Order and laws on the devbox

1. Pull `develop`; `make host-install` + `pfm install --yes` on the devbox is GOD's call there (it is not the Mac).
2. Task 0 (small, red-first) → Task 3 (foundation everything else builds on) → Task 1 → Task 2.
3. Same laws as the train: fence for every build/test of `pfm/`, regression tests watched red first, gitter is the only git writer, `.claude/**` / `templates/**` / any `CLAUDE.md` through `/pcm`, push only on the user's explicit ask, nothing identifying in tracked files.
