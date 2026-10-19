# Isolated Dev Foundation — worktree + container for pfm development

**Status:** APPROVED 2026-08-20 — rulings: (a) markdown-only waves stay on `develop` (`main` is release-only, moved by the release PR), code changes only in `.worktrees/`; (b) the mirror runs when a fenced wave fully closes — QA pass, orchestrator review with issues fixed, stable — then gitter merges and the host build runs; (c) one image for dev+e2e, files edited on the HOST worktree, the container mounts it for build/test only, and `infra/` at root holds the compose so the setup replicates every time.

## Problem

pfm development happens on the live checkout and ships straight onto the live box. `go build -o ~/.local/bin/pfm` swaps the binary the user's fleet tooling is running mid-session; `pfm install` rewires the live `~/.claude` settings, hooks, and shim; the e2e harness and jail tests, although jailed, share the box with the user's real work. Evidence from one evening: the CLI verb surface changed (#20) while live docs and muscle memory still called the retired forms; the live binary was swapped twice mid-session; a defect in picker synthesis shipped onto the box and produced fleet-invisible chats while the fleet manager itself was being rebuilt underfoot. The instrument being developed IS the instrument the user is holding.

This mirrors the pattern already proven in the user's other projects: development lives in a worktree with a fully isolated environment (own database, own container, own ports) and only verified work mirrors out.

## The fence — four layers

### 1. Worktree — code never changes on the live checkout

All train/code work happens in a `git worktree` under `.worktrees/{train-or-wave}/` (gitignored). The main checkout is the user's stable instrument: its binary, docs, and prompts change only by a gitter merge of verified work. The builder seat's cwd IS the worktree.

### 2. Container — one shared machine, a fresh run inside it

One long-lived dev container (`ubuntu:24.04` + zsh/tmux/git, pinned Go 1.27, and pinned Node 22) per host — per main checkout and image key — serves every fenced call of every worktree (`infra/fence/shared.sh`). The first caller starts it; only its creation is serialized, by a short lock, and runs never queue behind each other. Every call enters it with `docker exec` and runs inside namespaces of its own (`infra/fence/shared-run.sh`):

- Own pid and mount namespace, own `$HOME`, `/tmp`, `/var/tmp` and `/dev/shm`, removed when the run ends — `pfm install --yes` runs against that HOME, doctor runs in it, the shim sources into its zshrc. A process scan or a `pkill` sees this run alone.
- Own network namespace, loopback only, for every run, so a fixed test port never collides and the gate's egress verdict covers its own run alone; only `install`, which fetches from outside, and the interactive `iso shell` share the container's network, and `iso run` does only under `PFM_FENCE_NET=shared`.
- Own tmux server, own socket dir — fleet experiments (spawn/call/capture) run against run-local chats, never the live `cc-*`/`cx-*` sockets. The live-box law is physically satisfied, not just promised.
- Nothing publishes a port to the host unless a task explicitly asks.
- Builds land in the run's `~/.local/bin`, never the host's.
- Knobs, set per call by env, their defaults in `infra/fence/shared.sh`: `PFM_FENCE_CPUS` and `PFM_FENCE_MEM`, the container's CPU and memory caps, shared by every live run; `PFM_FENCE_TEST_P`, `go test -p`, default the CPU cap; `PFM_FENCE_IDLE_S`, after which a container with no live run exits; `PFM_FENCE_HEARTBEAT_S`, after which a run whose caller stopped beating is killed, so a dead caller's run dies within 5 s.
- A new image key starts a new container. A worktree whose key changed leaves its old key's container; an old container no worktree uses any more drains its live runs, then exits, and two worktrees on different keys each keep their own warm container. Fence housekeeping runs detached, only when a container starts.
- Timing artifacts land on the host under `/tmp/{project}/fence/shared/{worktree}/timing/`.

### 3. The real-simulation fence — live traffic without the host

`dev.sh iso sim '<command>'` runs the `pfm-sim` target of the same image (`infra/fence/pfm-dev.Dockerfile`): pfm-dev plus what a real desktop brings to live traffic.

- Google Chrome from Google's apt repo, native on amd64 and arm64. The harvester's browser sidecar launches only Patchright's `chrome` channel, so Chromium would report MISSING. Google keeps only the current release, so Chrome is the fence's one unpinned tool; its version prints in every run's proof line.
- Real fonts and 2 GB of `/dev/shm`, and no display: the harvester's browser renders headless only. Docker's 64 MB default crashes renderers on heavy pages.
- `infra/fence/sim-entry.sh` bootstraps each run: it builds `pfm` from the worktree, runs `pfm install` into the container HOME, and writes `harvester.config.json` with `fetch.browser` on, the same opt-in a person uses. Any failed step prints `sim: BOOTSTRAP-FAILED — <step>` with its log tail, and the command never runs.
- Harvester state (the converter and browser sidecars) lives in a volume keyed by the worktree path, `pfm-sim-harvest-<worktree>-<cksum>`. It provisions once per worktree, and each worktree's atomically published `current` environment is never flipped by another. uv's package cache lives inside the same volume, so uv hardlinks: every sidecar-script edit publishes a new environment (the script is part of its digest) in seconds, instead of copying the full environment per edit. `dev.sh iso sim-reset` drops the volume; fence housekeeping (`infra/fence/housekeeping.sh`, step `checkouts`) removes it once its worktree is gone.

### 4. The mirror — landing on live is explicit and last

gitter merges worktree → develop only after in-fence verification (project gates + walker). The live install (`go build -o ~/.local/bin/pfm` + `pfm install --yes` on the host) is a separate, user-gated mirror step — never a side effect of a task finishing. "Ship = installed" is redefined: ship = installed **in the container**; the host install is the mirror.

## Mechanics

- `dev.sh` provides `iso {install|build|typecheck|verify|test|cover|all|status|e2e|shell} [project]`, `iso sim-reset`, `iso gate` (the full gate, also `.claude/scripts/test.sh --full`) and `iso {run|sim} <command…>`; `.claude/scripts/test.sh <path>…` runs the affected tests and static checks of the named paths in the same fence. Every action but `iso sim` is a `docker exec` into the shared container; `iso sim` is a Docker Compose run of its own `pfm-sim` container per call. The active checkout is read-only; Go and golangci-lint caches use container volumes. The engine-sync source (`infra/fence/fence-env.sh`: `$ENGINE_SYNC_SRC`, else the worktree's own `plugins/engine-sync` when it holds a `go.mod` — that checkout as it stands, uncommitted edits included, not necessarily the commit the tree under test pins — else the main checkout's) is mounted read-only at `/engine-sync`, and `infra/fence/engine-sync.sh` builds from it the binary pfm execs for every Codex and OpenCode artifact; an absent source is named by that build, never skipped. An invocation builds the image only when the key of its build inputs (the build context's files, the resolved Compose build block and the platform) differs from the image's `pfm.fence.inputs` label. The base image is pinned by digest; a current image starts without a build or registry lookup. A network that blocks `registry.npmjs.org` exports `PFM_NPM_REGISTRY={mirror URL}` before the image builds; the image's npm installs then fetch the lockfile's packages from that mirror, integrity unchanged. Docker absent → loud `TOOLCHAIN-MISSING`, never a silent host fallback.
- **Broken-state report:** every `iso` run prints the container hostname, the run's `$HOME`, its working directory and its run id as its first line (an `iso sim` run prints its `sim:` line first, and a BOOTSTRAP-FAILED run stops before the command and its fence line) — a run that cannot prove it is inside the fence did not run inside the fence. A host-toolchain fallback is impossible by construction (the verb IS the docker invocation).
- Builder briefs change one clause: all build through `dev.sh iso`, all test through `.claude/scripts/test.sh` under the rule "The full gate runs ONCE per worktree, right before its merge. During development you run ONLY the tests your change affects. No full suite at any other time: not per repair round, not to verify a fix, not before the gate. A red gate is fixed with affected tests only, then the gate runs once more before the merge."; never build to the host's `~/.local/bin`; never run `pfm install` on the host.
- The former CLAUDE.md § Process "no worktree pipeline — deliberate scope choice" clause is reversed for code waves — § Process now builds code flights inside the fence (a /ptm change, ordered by the user 2026-08-20). Blueprint/docs-only waves are markdown and cannot destabilize the box — see decision (a).

## Unchanged

gitter-only git writes; the leak gate; the publication boundary; flight verification order.

## Sequence

1. Stabilize: commit the current verified state on main (this design lands with it).
2. Build the foundation as its own wave: container image + `dev.sh iso` + worktree setup + builder re-brief.
3. The train resumes inside the fence: #21 → #22 → #11-close, then S7–S10.

## Open decisions (the user's)

a. **Markdown-only waves** (templates/docs, e.g. #14) — stay on main, or also fenced through the worktree? b. **Live-install mirror trigger** — only on the user's explicit command, or automatically once a train fully closes (S10-equivalent)? c. **Container flavor** — extend the #11 e2e image (one image for dev and e2e), or a separate devcontainer definition?
