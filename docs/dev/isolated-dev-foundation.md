# Isolated Dev Foundation — worktree + container for pfm development

**Rulings:** (a) markdown-only waves stay on `develop` (`main` is release-only, moved by the release PR), code changes only in `.worktrees/`; (b) the mirror runs when a fenced wave fully closes — QA pass, orchestrator review with issues fixed, stable — then gitter merges and the host build runs; (c) one image for dev+e2e, files edited on the HOST worktree, the container mounts it for build/test only, and `infra/` at root holds the compose so the setup replicates every time.

## Contents

- [Problem](#problem)
- [The fence — four layers](#the-fence--four-layers)
- [Mechanics](#mechanics)
- [Fence knobs](#fence-knobs)
- [Unchanged](#unchanged)

## Problem

pfm development happens on the live checkout and ships straight onto the live box. `go build -o ~/.local/bin/pfm` swaps the binary the user's fleet tooling is running mid-session; `pfm install` rewires the live `~/.claude` settings, hooks, and shim; the e2e harness and jail tests, although jailed, share the box with the user's real work. Evidence from one evening: the CLI verb surface changed (#20) while live docs and muscle memory still called the retired forms; the live binary was swapped twice mid-session; a defect in picker synthesis shipped onto the box and produced fleet-invisible chats while the fleet manager itself was being rebuilt underfoot. The instrument being developed IS the instrument the user is holding.

This mirrors the pattern already proven in the user's other projects: development lives in a worktree with a fully isolated environment (own database, own container, own ports) and only verified work mirrors out.

## The fence — four layers

### 1. Worktree — code never changes on the live checkout

All train/code work happens in a `git worktree` under `.worktrees/{train-or-wave}/` (gitignored). The main checkout is the user's stable instrument: its binary, docs, and prompts change only by a gitter merge of verified work. The builder seat's cwd IS the worktree.

### 2. Container — one shared machine, a fresh run inside it

One long-lived dev container (`ubuntu:24.04` + zsh/tmux/git, pinned Go 1.27, and pinned Node 22) per host — per main checkout and image key — serves every fenced call of every worktree (`infra/fence/shared.sh`). The first caller starts it; creation, admission, a caps update and each run's live reservation are serialized under one short host start lock, and a caps update never lands beneath another live run (§ Caps contract); executions overlap freely and never queue behind each other. Every call enters it with `docker exec` and runs inside namespaces of its own (`infra/fence/shared-run.sh`):

- Own pid and mount namespace, own `$HOME`, `/tmp`, `/var/tmp` and `/dev/shm`, removed when the run ends — `pfm install --yes` runs against that HOME, doctor runs in it, the shim sources into its zshrc. A process scan or a `pkill` sees this run alone.
- Scratch is RAM when the container has room, else disk: each `shared-run.sh` call first reads cgroup v2 `memory.max` and `memory.current` (under `PFM_FENCE_CGROUP_ROOT`, default `/sys/fs/cgroup`) in `scratch_uses_ram` and needs free memory of at least the sum of the six tmpfs caps plus `PFM_FENCE_HEADROOM_MIB`, computed per call from the scratch knobs (defaults: 1874 + 1024 = 2898 MiB). With room it mounts capped tmpfs on `/root` (default 256 MiB, mode 0700), `/tmp` (1536 MiB, 1777) and `/var/tmp` (16 MiB, 1777); when free memory is low or unreadable it binds the run's own disk directories (`home`, `tmp`, `vartmp`, modes 0700, 1777, 1777) over those three paths instead and prints one `shared-run: disk scratch:` line on stderr naming free and needed MiB (a failed read says `cgroup read failed` and `free unknown MiB`). In both cases `/dev/shm` stays a 64 MiB (default), 1777 tmpfs and the `STATE` (`/var/fence`) and `MNT` (`/pfm-fence`) paths are hidden behind 1 MiB (default), 0700 tmpfs each. Every tmpfs mount uses `noswap`, so an unsupported kernel fails closed instead of spilling to swap, and its pages count against the shared container's memory cap (5 GiB desktop, 12 GiB native, whose swap cap is zero: `--memory-swap` equals `--memory`). The persistent Go module, build and lint caches stay on disk under `/cache/{gomod,go-build,lint}`, so scratch never forces a cold rebuild. A run's disk is opt-in: `/pfm-disk`, exported as `PFM_FENCE_DISK_DIR`, is a per-run directory removed when the run exits, for tests that need real device or durability semantics; production `fsync` and durability tests are not disabled.
- Own network namespace, loopback only, for every run, so a fixed test port never collides and the gate's egress verdict covers its own run alone; only `install`, which fetches from outside, and the interactive `iso shell` share the container's network, and `iso run` does only under `PFM_FENCE_NET=shared`.
- Own tmux server, own socket dir — fleet experiments (spawn/call/capture) run against run-local chats, never the live `cc-*`/`cx-*` sockets. The live-box law is physically satisfied, not just promised.
- Nothing publishes a port to the host unless a task explicitly asks.
- Builds land in the run's `~/.local/bin`, never the host's.
- Every run-shaping value is a knob in `fence_config_schema` (`infra/fence/shared.sh`), set per call by env — § Fence knobs.
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

- `dev.sh` provides `iso {install|build|typecheck|verify|test|cover|all|status|e2e|shell} [project]`, `iso sim-reset`, `iso gate` (the full gate, also `.claude/scripts/test.sh --full`) and `iso {run|sim} <command…>`; `.claude/scripts/test.sh <path>…` runs the affected tests and static checks of the named paths in the same fence. Every action but `iso sim` is a `docker exec` into the shared container; `iso sim` is a Docker Compose run of its own `pfm-sim` container per call. The active checkout is read-only; Go and golangci-lint caches use container volumes. The engine-sync source (`infra/fence/fence-env.sh`: `$ENGINE_SYNC_SRC`, else the worktree's own `plugins/engine-sync` when it holds a `go.mod` — that checkout as it stands, uncommitted edits included, not necessarily the commit the tree under test pins — else the main checkout's) is mounted read-only at `/engine-sync`, and `infra/fence/engine-sync.sh` builds from it the binary pfm execs for every Codex and OpenCode artifact; an absent source is named by that build, never skipped. An invocation builds the image only when the key of its build inputs (the build context's files, the resolved Compose build block and the platform) differs from the image's `pfm.fence.inputs` label. The base image is pinned by digest; a current image starts without a build or registry lookup. Docker absent → loud `TOOLCHAIN-MISSING`, never a silent host fallback.
- `PFM_NPM_REGISTRY` is an optional image-build argument, empty by default, for dev, sim and demo images and release rehearsal. Compose passes `${PFM_NPM_REGISTRY:-}` to `pfm-dev`; `pfm-sim` inherits it, and `infra/demo/up.sh` builds through the same Compose service. `infra/fence/release-rehearsal.sh` passes `--build-arg` only when nonempty and retains its named image-build failure. The shared tools-install `RUN` in `infra/fence/pfm-dev.Dockerfile` conditionally exports `NPM_CONFIG_REGISTRY` for that install only; package pins and integrity metadata remain unchanged. Empty leaves npm's default registry unchanged, and host npm policy is untouched. The image-input key includes resolved build arguments, so changing the registry invalidates the image. Mirror network installation is not verified; this override does not guarantee reachability or successful installation.
- **Broken-state report:** every `iso` run prints the container hostname, the run's `$HOME`, its working directory and its run id as its first line (an `iso sim` run prints its `sim:` line first, and a BOOTSTRAP-FAILED run stops before the command and its fence line) — a run that cannot prove it is inside the fence did not run inside the fence. A host-toolchain fallback is impossible by construction (the verb IS the docker invocation).
- Builder briefs change one clause: all build through `dev.sh iso`, all test through `.claude/scripts/test.sh` under the rule "The full gate runs ONCE per worktree, right before its merge. During development you run ONLY the tests your change affects. No full suite at any other time: not per repair round, not to verify a fix, not before the gate. A red gate is fixed with affected tests only, then the gate runs once more before the merge."; never build to the host's `~/.local/bin`; never run `pfm install` on the host.
- CLAUDE.md § Process builds code flights inside the fence; blueprint/docs-only waves are markdown, cannot destabilize the box, and land on `develop` directly — ruling (a).

## Fence knobs

`fence_config_schema` (`infra/fence/shared.sh`) is the one source of run-shaping defaults, one row per knob with four required fields: `name|desktop-default|type|native-default`. A native field of `=` inherits the desktop default; a literal empty native field is an empty native default. Tuning native is one edit to that field — `PFM_FENCE_CPUS|6|cpu|12` to `PFM_FENCE_CPUS|6|cpu|8` — and leaves desktop unchanged. `.claude/scripts/dev.sh` sources `shared.sh` and runs `fence_config_init` before it sources `steps.sh` and `checks.sh`, so the resolved profile picks every default before either fills one: on `iso` always, and on `test`, `e2e`, `all` and `gate` inside the fence; `status`, `build` and `install` never query Docker. `fence_config_init` resolves the profile, then `fence_config_values` fills every unset name with the default of the resolved profile — the native field only under `PFM_FENCE_PROFILE=native` — validates it by type and exports it. An invalid supplied value exits non-zero with `iso: {name} must be {type} (got '{value}')` before any image build, container start or run — never a silent fallback. A name set in the environment, even to empty, keeps its value: an individual env override beats the profile.

### PFM_FENCE_PROFILE

`desktop`, `native` or `auto` (default `auto`); an explicit `desktop` or `native` wins. `auto` reads `docker info` — `OperatingSystem`, `NCPU`, `MemTotal`, never the hostname: an OS naming `Desktop` is always `desktop`; a native Linux (`Linux`, `Ubuntu`, `Debian`, `Fedora`) is `native` only when the daemon's `NCPU` and `MemTotal` cover the native defaults of `PFM_FENCE_CPUS` and `PFM_FENCE_MEM` (12 CPUs, 12g), else `desktop`, so a smaller native daemon keeps the conservative desktop defaults. A failed `docker info`, a CPU or memory report that is not a positive integer, an unsupported OS or any other profile value is a named `iso: PFM_FENCE_PROFILE …` error. Four native fields differ from desktop — `PFM_FENCE_CPUS` 12, `PFM_FENCE_MEM` 12g, `PFM_FENCE_TEST_P` 6, `STEPS_JOBS` 6 — and every other row inherits (`=`). Native therefore runs a 12-CPU, 12g container with unit `go test -p 6` and 6 concurrent steps; `PFM_FENCE_CPUSET` and `PFM_FENCE_GOMAXPROCS` stay empty and `STEPS_HEAVY_JOBS` stays 2. Desktop keeps the measured baseline (6 CPUs, 5g, scheduler auto). Each declared env override wins over either profile.

The native values are measured on the full gate: the prior 6-CPU, 5g baseline over three green runs had a median wall of 165.8 s (range 164.3–169.3 s) and a median CPU of 894.5 s (range 882.8–902.9 s); the shipped native values over three green runs a median wall of 113.0 s (range 112.7–114.0 s) and a median CPU of 895.5 s (range 876.0–905.5 s) — about 31.8% less wall for unchanged CPU work. A 12-CPU cpuset ran faster warm (103 s), but a cpuset names machine-specific CPU IDs, so native ships none. A 12-CPU unbounded-scheduler candidate with an explicit `GOMAXPROCS` flaked on deadlines and is rejected. Each run's `fence:` line names `profile=`, `cpus=`, `mem=`, `cpuset=` and `GOMAXPROCS=` (`inherited` when unset).

### Types

| Type | Valid value |
| -------------- | ------------------------------------------------------------------------------------------ |
| `uint` | `0`, or a whole number without leading zeros up to 999999999 (Bash arithmetic would read `08` as octal) |
| `int` | positive integer, 1–999999999 |
| `optional_int` | empty, or an `int` |
| `number` | positive decimal, at most 1000000000 |
| `cpu` | decimal 0–1000000; `0` sets no CPU quota |
| `size` | strictly positive: a raw byte count of up to 18 digits (Docker inspect output such as `5GiB` as bytes), or up to 9 digits with a `k`, `m` or `g` suffix |
| `cpuset` | empty, or a Docker CPU list (`0-3,6`): ranges ascending, each CPU at most 1000000 |
| `duration` | positive integer with `ms`, `s`, `m` or `h` |
| `trace` | `err`, `x` or `off` |
| `network` | `private` or `shared` |
| `budget` | `unpinned` (record the wall, never judge it), or an `int` |

### Knob rows

Each default shown is the desktop default; native differs only where the row names it, every other native field inherits (`=`).

| Group | Knob (default) · type | Meaning |
| ------------- | --------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Caps | `PFM_FENCE_CPUS` (6; native 12) · cpu; `PFM_FENCE_MEM` (5g; native 12g) · size; `PFM_FENCE_CPUSET` (empty) · cpuset | shared container CPU quota, memory cap (also `--memory-swap`: no swap), pinned CPUs (empty: unpinned) |
| Go | `PFM_FENCE_GOMAXPROCS` (empty) · optional_int | exported as `GOMAXPROCS` into each run; empty leaves it unset, inherited |
| Go | `PFM_FENCE_TEST_P` (empty; native 6) · optional_int; `PFM_FENCE_TEST_PARALLEL` (4) · int | unit `go test -p` (empty: the CPU cap) and `-parallel` |
| Go | `PFM_FENCE_TEST_COUNT` (1) · int; `PFM_FENCE_TEST_TIMEOUT` (25m) · duration | `go test -count`; unit `-timeout` |
| Go | `PFM_FENCE_AFFECTED_P` (empty), `PFM_FENCE_AFFECTED_PARALLEL` (empty) · optional_int; `PFM_FENCE_AFFECTED_TIMEOUT` (10m) · duration | affected-test (`.claude/scripts/test.sh`) `-p`, `-parallel` (empty: Go's default), `-timeout` |
| Go | `PFM_FENCE_E2E_P` (1) · int; `PFM_FENCE_E2E_PARALLEL` (empty) · optional_int; `PFM_FENCE_E2E_TIMEOUT` (25m) · duration | tagged e2e `-p`, `-parallel` (empty: Go's runtime default), `-timeout` |
| Go | `PFM_FENCE_RACE_TIMEOUT` (10m) · duration | race step `-timeout` |
| Go | `PFM_FENCE_SHARDS_CMD` (4) · int; `PFM_FENCE_SHARDS_UPDATE` (2) · int | test shards of `cmd/pfm` and `internal/update` (`pfm/scripts/test-shard.sh`) |
| Lifecycle | `PFM_FENCE_NET` (private) · network | `iso run` network: `private` is loopback only, `shared` the container's |
| Lifecycle | `PFM_FENCE_IDLE_S` (1800) · int; `PFM_FENCE_DAEMON_POLL_S` (2) · number | container with no live run exits after this long; its idle daemon's poll |
| Lifecycle | `PFM_FENCE_HEARTBEAT_S` (4) · number; `PFM_FENCE_BEAT_S` (0.5) · number | run killed after this long without a beat; feeder beat interval |
| Lifecycle | `PFM_FENCE_LOCK_TICKS` (3000) · int; `PFM_FENCE_LOCK_POLL_S` (0.2) · number | start-lock wait: ticks × poll (600 s) |
| Lifecycle | `PFM_FENCE_IMAGE_ATTEMPTS` (3), `PFM_FENCE_START_ATTEMPTS` (10), `PFM_FENCE_EXEC_ATTEMPTS` (2) · int; `PFM_FENCE_RETRY_S` (2) · number | image, container-start and `docker exec` tries; pause between |
| Lifecycle | `PFM_FENCE_START_RETRY_S` (1) · number; `PFM_FENCE_GONE_TICKS` (10), `PFM_FENCE_FEED_GRACE_TICKS` (30) · int | pause before retrying a start whose name is still in use; lock-poll ticks waiting for a stopped container to go; 0.1 s ticks the feeder waits for the CLI after the caller dies |
| Scratch | `PFM_FENCE_HOME_MIB` (256), `PFM_FENCE_TMP_MIB` (1536), `PFM_FENCE_VARTMP_MIB` (16), `PFM_FENCE_SHM_MIB` (64) · int | tmpfs caps of `/root`, `/tmp`, `/var/tmp`, `/dev/shm` |
| Scratch | `PFM_FENCE_HIDE_MIB` (1), `PFM_FENCE_HEADROOM_MIB` (1024) · int | each `STATE`/`MNT` hide tmpfs; free-memory margin above the caps for RAM scratch |
| Compose | `PFM_FENCE_COMPOSE_MEM` (3g), `PFM_FENCE_SIM_SHM` (2g) · size | Compose service memory and swap cap; `pfm-sim` `/dev/shm` |
| Gate | `PFM_FENCE_GATE_ALL_S` (198), `PFM_FENCE_GATE_PFM_S` (198), `PFM_FENCE_GATE_TEMPLATES_S` (32) · int | gate wall budget per target |
| Gate | `PFM_FENCE_GATE_TOLERANCE` (1.25), `PFM_FENCE_GATE_FAIL_FACTOR` (2) · number | warn above budget × tolerance; fail beyond fail_factor × limit |
| Gate | `PFM_FENCE_STEP_DEFAULT_S` (1500) · int | per-step bound when the budget gives none |
| Gate | `EGRESS_LISTEN_WAIT_S` (5), `EGRESS_END_WAIT_S` (5) · number | egress probe: listener-ready and end-marker waits |
| Gate | `SKILL_TEST_SHARDS` (empty) · optional_int; `PFM_FENCE_SKILL_SHARD_MAX` (8) · int | skill-suite unittest shards (empty: the cores, at most the max) |
| Gate | `PFM_FENCE_HISTORY_LOCK_TICKS` (1200) · int | gate-history ledger append-lock wait, 0.1 s ticks (120 s) |
| Steps | `STEPS_JOBS` (empty; native 6) · optional_int; `STEPS_HEAVY_JOBS` (2) · int | step concurrency (empty: CPU quota rounded up, else `nproc`); heavy steps at once |
| Steps | `STEPS_BOUND_S` (empty) · optional_int | per-step bound (empty: the target's budget fail limit) |
| Steps | `STEPPROF_GRACE_TICKS` (20) · uint; `STEPPROF_TRACE` (err) · trace | 0.1 s ticks from TERM to KILL; xtrace mode (`err` failing commands, `x` every command, `off`) |
| Steps | `SAMPLER_INTERVAL_S` (0.5) · number; `SAMPLER_SPIN_ITERS` (10000) · int | sampler row interval; spin-probe loop length |
| Steps | `PFM_FENCE_SAMPLER_READY_TICKS` (50) · int | resource-sampler readiness wait, 0.1 s ticks (5 s) |
| Housekeeping | `PFM_FENCE_CONTAINER_MAX_HOURS` (6), `PFM_FENCE_BUILDCACHE_HOURS` (168), `PFM_FENCE_BUDGET_MINUTES` (120), `PFM_FENCE_SCRATCH_DAYS` (7) · int | forced container age; build-cache age; cache-budget check interval; scratch age |
| Housekeeping | `PFM_FENCE_BUILDCACHE_MINUTES` (1440) · int | interval between builder-prune checks, apart from the `PFM_FENCE_BUILDCACHE_HOURS` age |
| Housekeeping | `PFM_FENCE_GOCACHE_MB` (15000), `PFM_FENCE_GOMOD_MB` (4000), `PFM_FENCE_LINTCACHE_MB` (2000), `PFM_FENCE_UVCACHE_MB` (8000), `PFM_FENCE_HARVESTCACHE_MB` (8000) · int | cache volume budgets |
| Housekeeping | `PFM_FENCE_DF_TIMEOUT_S` (120) · int | bound on `docker system df -v` |
| Lanes | `LANE_EGRESS_READY_SECS` (10), `LANE_EGRESS_VERDICT_SECS` (30) · uint; `LANE_STORM_WAIT_SECS` (60) · int | egress ready and verdict waits; storm wait |
| Lanes | `LANE_ALIVE_RETRY_SECS` (1), `LANE_WAIT_LAST_EVERY_SECS` (0.5), `LANE_WAIT_FOR_EVERY_SECS` (0.2), `LANE_STORM_EVERY_SECS` (0.5) · number | lane wait polls; the two storm lanes' own poll, separate from the 0.2 s wait-for poll |
| Lanes | `PFM_FENCE_LANE_ROOT_LOCK_TICKS` (18000) · int | root-build lock wait in `PFM_FENCE_LOCK_POLL_S` ticks (3600 s at 0.2 s) |
| Lanes | `PFM_FENCE_LANE_TOLERANCE` (1.25) · number; `PFM_FENCE_LANE_SEQUENCE_S` (763) · int | lane budget tolerance; whole-sequence wall budget |
| Lanes | `PFM_FENCE_LANE_O1_S` (70), `PFM_FENCE_LANE_E1_S` (194), `PFM_FENCE_LANE_E2_S` (39), `PFM_FENCE_LANE_E3_S` (14), `PFM_FENCE_LANE_F_S` (134), `PFM_FENCE_LANE_M_S` (156), `PFM_FENCE_LANE_A_S` (49), `PFM_FENCE_LANE_O2_S` (86) · int; `PFM_FENCE_LANE_H_S` (unpinned) · budget | per-lane wall budgets |

The budget rows of `infra/fence/gate-budget.yml` and `infra/fence/lanes/budgets.yml` name these env knobs, resolved through the schema; the defaults are the pinned walls. `docker-compose.yml` reads `PFM_FENCE_COMPOSE_MEM` and `PFM_FENCE_SIM_SHM`, required set by `fence_config_init`. The start, image and exec retry loops count their attempts knob. The established `STEPS_`, `STEPPROF_`, `SAMPLER_` and `LANE_` names stay as they are, with no `PFM_FENCE_` alias.

### Caps contract

The caps are the one shared container's, shared by every live run. Admission, the caps update and the run's live reservation (`_fence_sh_reserve`) run under the same host start lock. A caller whose caps differ changes the container only when no other run is live; otherwise it changes nothing, prints `fence: CAPS CONFLICT requested cpus=… mem=… cpuset=… — running under live cpus=… mem=… cpuset=… (another run is live)` and runs under the live caps. There is no refusal option.

An idle container takes the new caps one of two ways. Docker update ignores a zero CPU quota (`PFM_FENCE_CPUS=0`) and an empty cpuset, so a request that clears a nonzero live quota or a nonempty live cpuset removes and recreates the idle container (`fence: recreating idle {name} to clear CPU quota or cpuset`); the cache volumes and timing data survive the recreation, and a refused removal warns and keeps the live caps. Every other difference goes through `docker update`, after which the container is inspected again: the receipt names the requested caps only when every field applied, else `WARN fence: … update did not apply every requested cap — using inspected live caps …` and the run records those effective caps.

A host run record whose caller is dead is reaped (`_fence_sh_reap_runs`) only after its PID-1 live reservation is cleared in every running shared container matching the same roots, across image keys. A failed container enumeration or cleanup warns and retains the host record for the next retry.

Every record carries the effective caps, never the requested: each run writes `caps.tsv` (`profile`, `cpus`, `mem`, `cpuset`) into its run directory and copies it to the host `.runs/{id}/`; `gate.meta` records `profile`, `cpus`, `mem`, `cpuset` and `gomaxprocs`; `infra/fence/gate-history.sh` appends `worktree`, `run` and those five to the ledger sidecar `caps.tsv`, failing with `gate-history: LEDGER-NOT-WRITTEN` when it cannot.

### Fixed by design

These values are hardcoded on purpose, not tuning knobs:

- The canonical lane sequence runs serially, concurrency 1: its lanes accumulate shared state.
- Per-beat storm counts, fixtures and asserted test timing values are scenario data.
- Units, tmpfs modes (0700, 1777), namespace safety flags, paths, ports, cache identities, version pins and signal semantics.
- Per-package `.testtiming.yml` ceilings are regression policy, kept as declared budget files.
- The profiler's 0.1 s and egress readiness's 0.05 s polls are protocol resolution; parser output truncation and display widths.

## Unchanged

gitter-only git writes; the leak gate; the publication boundary; flight verification order.
