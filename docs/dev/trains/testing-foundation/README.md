# Train — testing-foundation

**Status:** DESIGN → BUILD (devbox, 2026-09-17). Branch `wave/testing-foundation`, worktree `.worktrees/testing-foundation`, base `develop @ 8bfb6cdc`.
**Source ask:** `docs/dev/trains/queue/2026-09-17-e2e-realism-fence-and-reload.md` plus the user's brief on the devbox — "a safe and sound unit/integration testing for pre-release … so we won't disappoint people", "a foundation in its concurrency with sweet-spot investigation so tests won't be the next bottleneck", "cover the whole landscape — the answer is overlapping of areas and edge cases".

## The two promises

1. **Nothing ships that disappoints an adopter.** Every functionality item in the inventory (`waves/*/inventory/*.md`, derived from code, not docs) maps to at least one test that FAILS when that item breaks, and the map itself is a gate: an unmapped item is a named hole, never silence.
2. **Tests never become the bottleneck.** Every suite carries a timing budget that is measured on every run and ratchets down, never up (`waves/2-timing-ratchet/spec.md`). The inner loop (`make test` on the host) stays under one minute; the full pre-release gate runs its lanes in parallel and finishes in the time of its slowest lane, not the sum.

## Tiers — one name each, used everywhere

| Tier | What runs | Where | Mocks | Seats/tokens | Gate |
| --- | --- | --- | --- | --- | --- |
| **U** unit | Go packages, `t.Parallel`, jailed HOME | host (`make test`) and fence | every host/account/API/process door mocked at its seam | none | every commit |
| **A** hermetic e2e | the real binary driven end to end (`pfm/e2e`, tag `e2e`) | fence | none — real filesystem, fake HOME, no engines | none | every commit |
| **B** live DFS | the real product on a fresh machine, real engines, real project | fence container(s) built from one seeded root image | none | yes, budgeted per lane | wave close + pre-release |
| gates | fmt · lint-new · arch C1–C21 · clone · leak · manifest · timing ratchet | fence + CI | — | — | every commit |

Tier U is the developer's loop; Tier A proves the binary; Tier B proves the product. A behaviour proven only in B is a U/A gap to close, not a reason to run B more often.

## Concurrency — the foundation, not an afterthought

The sweet spot is measured, never assumed. Three knobs, one protocol (`waves/2-timing-ratchet/spec.md`):

- **Tier U**: `go test -p N` (packages in parallel) × `-parallel M` (tests inside a package) × `GOMAXPROCS`, measured cold-cache and warm-cache, on the fence (15 vCPU) and on the host. The knee — where wall time stops falling — is the pin; the serial hotspots (tests that sleep, spawn real processes, or hold a global lock) are named and either mocked (→ U) or moved (→ A).
- **Tier A**: `-p 1` today because the suite builds and installs one binary into one jailed HOME; the spec measures whether per-test HOMEs let it parallelise, and pins the result.
- **Tier B**: the root (fence up → `pfm install` → Professor on express → empty fleet) is built ONCE into a seeded image; every lane starts its own container from it, so lanes run as parallel containers and the suite's wall time is the slowest lane. The true limit is seats: a lane that spends a seat declares it, and the runner schedules seat-spending lanes so no seat is shared by two live lanes.

A timing table is emitted by every suite run (`tmp/timing/<suite>-<stamp>.tsv`) and the pinned budgets live in `pfm/.testtiming.yml` (U/A) and `infra/fence/lanes/budgets.yml` (B); a run over budget is red with the offending package or lane named.

## Lanes — depth-first, deliberately overlapping

The northstar for lane design: cover the whole landscape efficiently, without a bottleneck, by letting lanes **overlap on the seams**. A lane is one user path walked to its end; two lanes that both cross a seam (reload-while-busy, a dropped seat, an expired token, a symlinked home) each exercise it from their own side — that overlap is where integration defects live, so it is planned, not accidental. The lane graph, its overlap matrix, and the item→lane→beat map live in `waves/4-integration-dfs/spec.md` and are derived from the inventories in `waves/4-integration-dfs/inventory/`.

## Waves

| # | Wave | Depends on | Ships |
| --- | --- | --- | --- |
| 0 | `0-reload-feedback` | — | truthful `/reload` hook answer + on-pane hold notice (diagnosed defect from the queue spec) |
| 1 | `1-infra-fence` | — | `infra/fence/` holds every fence mechanism; `infra/demo/` holds only demo content; `infra/readme-gif` removed; every consumer re-pointed |
| 2 | `2-timing-ratchet` | baseline measurements | `scripts/test-timing.sh` + budgets + the concurrency sweet-spot report; the ratchet wired into `dev.sh verify` |
| 3 | `3-unit-law` | seam map (`tmp/walks/unit-seams.md`) | seams + fakes for every NONE door, the ten host edge-case fixtures (`pfm/internal/hostfixture`), `t.Parallel` across packages, `TESTPLAN.md` § fixtures |
| 4 | `4-integration-dfs` | 1, inventories | seeded root image, lane runner, beat registry, item→lane map gate, the lanes themselves |
| 5 | `5-close` | 0–4 | reviewer over the whole range, timing report, docs (`docs/dev/testing/`), merge to develop |

Waves 0, 1, 2, 3 build in parallel (disjoint files; one worktree; gitter commits by pathspec, each wave's gate run on its own committed slice). Wave 4 starts when 1 lands.

## Laws (unchanged, restated once)

Fence for every `pfm/` build or test during a wave (`dev.sh iso …` from the worktree, relative path); a regression test is watched red before it counts; gitter is the only git writer; `.claude/**`, `templates/**`, any `CLAUDE.md` through `/pcm`; nothing identifying in tracked files; push only on the user's explicit ask; every agent report is verified against the artifact on disk before it is relayed.

## Prior art

Two research reports fed the wave specs (each spec carries its own § Prior art): `.professor/RR/agent-fleet-testing-release-chain-2026-09-17.md` — how Codex CLI, OpenCode, Goose, claude-squad, zellij, chezmoi, mise, Homebrew, aider, Cline, Claude Code and the MCP conformance suite test and gate releases (chezmoi's four-tier ladder is the closest template; no neighbour keys its merge gate on a live model; none documents a flaky quarantine) — and `.professor/RR/hermetic-trustworthy-test-gates-2026-09-17.md` — the literature on hermetic suites, flake economics (FSE 2014 / Google TAP), the four-tier release chain, `testscript`/`synctest`/pty seams, and honest known-gap ledgers (`xfail strict`); no source quantifies a parallelism sweet spot or a CI-time ratchet — those are this train's own measurements.
