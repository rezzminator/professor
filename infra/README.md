# infra — the fence, the demo, and the manifest gate

Three independent things share this directory; nothing here is one system.

```
infra/
  README.md                      this file — the map
  check-self-hosted-manifest.sh  repo gate, not fence-only (dev.sh verify templates calls it)
  fence/                         the isolated dev fence — build/test in one shared container, a fresh run per call
  demo/                          the live-demo fence — a real install for presentations
  readme-cards/                  the README section-card recorder (screenshots/motion clips)
```

## fence/

Development must never destabilize the live box: code changes happen in a git worktree under `.worktrees/`, and every build/test runs inside the one shared `pfm-dev` container (`shared.sh`), each call a `docker exec` with that worktree read-only at `/worktree` and its own namespaces (`shared-run.sh`) — own HOME and `/tmp`, own tmux, no published ports. Files are edited on the host; the container only builds and tests. Design and law: `docs/dev/isolated-dev-foundation.md`.

`docker-compose.yml` + `pfm-dev.Dockerfile` build the image (Go pinned to `pfm/go.mod`, Node to the mirror compilers' minimum); `fence-env.sh` resolves the `PFM_DEV_*` mount contract every caller sources; `tools.env` + `tools.sh` pin the dev tools `make tools` installs on the host and bakes into the image.

The same Dockerfile's `pfm-sim` target is the real-simulation fence: pfm-dev plus Google Chrome, which runs headless only, and 2 GB of shared memory. `sim-entry.sh` builds and installs `pfm` from the worktree, then uses the separately mounted plugin source to build the standalone `harvester` binary and run `harvester setup` with its browser rung on. Harvester state persists per worktree in `pfm-sim-harvest-<worktree>-<cksum>`, `dev.sh iso sim-reset` drops it, and fence housekeeping (`infra/fence/housekeeping.sh`) removes it once its worktree is gone.

`fence/lanes/` is the Tier B hermetic suite over the same image: `root.sh` builds a fixture-seated root with `provision.sh`, the mock engine and `adopt.sh`; `cred-scan.sh` refuses unregistered credentials; `hook-command.sh` reads a settings hook command in its plain form for lane A.11 and the demo's express-hooks beat. `run.sh` starts one offline container and runs the selected lanes in canonical order (`O1 → E1 → E2 → E3 → F → M → A → O2`), each beat asserting from pfm's own reports and the pane. `scenarios/` holds engine scripts, `lib.sh` the beat library, `check-map.sh` the coverage gate, `known-gaps.yml` and `budgets.yml` the ledgers, and `tests/` the harness tests. How to run one lane, read a red row and extend the suite: `docs/dev/testing/lanes.md`.

Entry point — from the worktree checkout:

```bash
.claude/scripts/test.sh <path>...        # the affected tests + static checks of those paths
.claude/scripts/test.sh --full           # the full gate, once, right before the merge
.claude/scripts/dev.sh iso run '<command>'  # a probe the test command does not cover
.claude/scripts/dev.sh iso shell         # interactive shell, a fresh run
.claude/scripts/dev.sh iso sim 'harvester --refresh --json https://example.com/'  # live traffic, real Chrome
```

A run builds the image only when its image key changed, then prints the fence proof (`fence: container=… HOME=/root work=… run=…`); a run that cannot print it did not run inside the fence. Docker absent reports `TOOLCHAIN-MISSING` — never a silent host fallback.

## demo/

A REAL Professor install inside the same fence image, for live presentations — real Claude Code + Codex + pfm, real chats over `chat-professor` and the native Harvester plugin. Entry point: `infra/demo/up.sh` (see `infra/demo/README.md`).

## check-self-hosted-manifest.sh

Verifies this checkout's own `.professor/manifest.json` against the tracked tree — `dev.sh verify templates` calls it. Not fence-only, so it stays at the top level rather than moving into `fence/`.
