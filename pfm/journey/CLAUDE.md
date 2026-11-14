# journey — pfm's live tier: one tree of owner steps typed into real terminals on a disposable machine, each checked against the state underneath

It runs as the owner on the machine's real paths, no jail, and every branch of the day shares that machine at once: a step that reads, replaces or races what a sibling owns turns that sibling red on some interleavings only, often on the macOS runner alone.

# Vocabulary

- tree: `install`, then every branch of `day` together, then the joins (`fleet`, `restart`, `install-again`, `doctor`, `uninstall`) alone · `tree_test.go`
- node path: a step's names from the root (`install/day/{branch}/{step}`): its run filter, its timeline row and its evidence directory
- `State`: what a step hands its children: its home, release, Herdr session, terminal, project and chats · `state_test.go`
- harness: the walker (`Do`, `Together`, `Run`), the step's `Ctx` and the PTY `Terminal`; product-agnostic, pfm's reads and the log gate plugging in through `Hooks`; tested in pfm's unit and jail tiers · `harness/`
- branch session: `herdrSession`, a Herdr branch's own `herdr --session {unique}`, ended with its chats by the owner's own commands when its subtree ends; `herdrSocket(s)` is its read scope · `step_herdr_session_test.go`
- whole-machine check: `fleetWhole`, the one reader of the unscoped `""` · `fleet_whole_test.go`
- log gate: `logGate` over pfm's activity log; `Ctx.Expect` declares an `ERROR` record · `fleet_test.go`
- directive: the prompt `mock-engine: {json steps}` the fake engine plays as one turn, built by `directive`, `turnStep`, `cli` and `mcpStep` · `step_call_test.go`, `doors_test.go`, `pfm/internal/mockengine/`
- OS twins: a `{name}_linux_test.go` and `{name}_darwin_test.go` pair per kernel difference (`machine_*`, `proc_*`, `services_*`); arch-check C32 counts no OS-selected file
- machine: `prepare.sh` gives the owner what a real one has before pfm (fake engines, zsh, Herdr config, a demo project, a second account) and installs nothing of pfm; `run.sh` boots it on Linux, the CI runner is it on macOS · `infra/fence/journey/`

# Runtime

## Machine

- Every terminal and `Ctx.Read` gets only `ownerVars` (`journey_test.go`), with `~/.local/bin` and `~/.local/share/journey/bin` first on `PATH`: a value pfm must see reaches the owner's login through `prepare.sh`, as `GOCOVERDIR` does.
- Bounds: a terminal wait 10 s unless given, `stepBound` 10 s, a `Ctx.Read` 2 min, a poll every 25 ms, the tree `budget`.
- Under a together node the log gate judges every branch once, over the node's whole window, against every branch's `Expect` patterns.
- `Ctx.Cleanup` runs once the node's whole subtree ends, failed or not, after its evidence and before its terminals close; a teardown failure is logged, never a verdict, and the joins judge what it left.
- Narrowest: `bash infra/fence/journey/run.sh -run 'Journey/{node path}$'`; done: the whole tree, then the macOS run `.claude/rules/pfm-os-doors.md` requires; flags, output and artifacts: `.claude/commands/pfm-testing-manual.md` § Run commands.

# On demand

- Before adding a branch or node, or changing `harness/` or `infra/fence/journey/`: read `docs/design/integration-suite/journey-tree.md`, whose § What it replaced names the refused shapes.

# Rules

## Screen

- After a keypress or a delivery, act on a settled frame: wait until the element you read has changed and reads the same on two polls (`pickEngine`); a Claude pane frame without its composer row is mid-repaint (`composerLine`) — a half-drawn frame sends a second key or matches an older row.
- A Herdr pane is narrow: a long row wraps or ends in `…`, so match a row's start or its name's stem (`rowIs`) and read the whole value from state.
- Once a terminal's shell ends, assert from state (the chat, its seat, engine and tmux server, the shell gone), not its last frame: macOS revokes the terminal before tmux restores the screen.
- Write a screen literal from a screen a run printed: a failed wait prints it whole, and evidence keeps `terminal-{name}.txt`.

## Shared machine

- A Herdr branch starts with `herdrSession` and hands `herdrSocket(s)` to every reader: two terminals on one session mirror each other and interleave their keys.
- Every `sqlite3` call on pfm's state database goes through `stateSQL` or `tryStateSQL`, which wait out the lock every branch and pfm contend for.
- Before a step runs `pfm install`, poll `jobsRunning` to empty: an install beside a running name-sync or schedule job is refused.
- Replace a file another branch may read with one `mv` of a written temp file; remove a Claude build under `~/.local/share/claude/versions/` only once a poll shows no launch still execing it, and keep the newest — a sibling's launch resolves the file mid-swap.
- Type commands both kernels' tools accept: `printenv` with two names, `stat -c` and `find -xtype` pass on Linux and fail on macOS; a kernel difference goes in an OS twin.

## Reds

- A wait that times out under load (sibling builders in the tree, a loaded host) is reproduced at rest before it is reported as a product fault; a bound or `budget` moves only once timings taken at rest explain the slowness.
