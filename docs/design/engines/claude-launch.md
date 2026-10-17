# Claude interactive launch

Every Claude Code chat pfm starts in a tmux pane is described by one table, the launch registry `claudelaunch.Knobs` (`pfm/internal/claudelaunch/knobs.go`), and rendered by one function, `claudelaunch.Render` (`pfm/internal/claudelaunch/render.go`). Every flag, environment variable and settings key pfm hands Claude at launch is a row there; nothing else in pfm spells one. This file holds the registry's shape, where each value comes from, how it reaches Claude, the code paths that launch a chat, the managed `claude` launcher, and the doctor audit. Headless runs are in [claude-headless.md](claude-headless.md); files pfm places on disk are in [claude-config-dir.md](claude-config-dir.md).

## Contents

- [Decisions](#decisions)
- [How Claude layers its settings](#how-claude-layers-its-settings)
- [The registry](#the-registry)
- [Wires](#wires)
- [Knobs](#knobs)
- [CheckConfigDir](#checkconfigdir)
- [Sources](#sources)
- [The launch record](#the-launch-record)
- [Config keys](#config-keys)
- [The rendered launch](#the-rendered-launch)
- [The system prompt](#the-system-prompt)
- [Doors](#doors)
- [The managed launcher](#the-managed-launcher)
- [The zsh shim](#the-zsh-shim)
- [Long launch lines](#long-launch-lines)
- [pfm config claude](#pfm-config-claude)
- [pfm doctor spawn-audit](#pfm-doctor-spawn-audit)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Decisions

- **One table owns every launch value.** To change what a Claude chat starts with, edit a row in `claudelaunch.Knobs` or a key in `pfm.config.json` — nowhere else. A test (`TestNoLaunchLiteralOutsideRegistry`) fails when any Go file outside `pfm/internal/claudelaunch/` emits a Claude launch flag or sets or unsets a `CLAUDE_CODE_*` / `ANTHROPIC_*` / `ENABLE_PROMPT_CACHING_*` / `FORCE_PROMPT_CACHING_*` variable for a Claude process; code that only reads a variable Claude itself sets (`CLAUDE_CODE_SESSION_ID`, `CLAUDE_CONFIG_DIR`) is not an emitter.
- **One `--settings` payload carries almost everything.** Environment variables ride its `env` block and every other setting rides its own key. `--settings` outranks the project and account settings files, and its `env` overwrites the shell's value, so a launch value cannot be shadowed by a project file or an exported variable.
- **Three things stay outside it.** `CLAUDE_CONFIG_DIR` is process environment because it chooses where Claude reads settings from; the unset list is `env -u` because a settings `env` block can set a variable but not remove one; `--system-prompt-file`, `--mcp-config` and the session verbs (`--session-id`, `--resume`, `--fork-session`, `--name`) are flags because no settings key replaces them; the autonomy pair, `--model` and `--effort` stay flags because a flag outranks its settings key.
- **Defaults come only from `pfm.config.json`.** A per-launch choice changes one launch; no door carries a default of its own ([pfm-home.md](pfm-home.md#decisions)).
- **Helper calls run on Haiku, always.** Claude Code's helper calls (the page summary behind WebFetch, session and tool-use-summary titles, a hook's prompt, and the WebSearch call while its server flag `tengu_plum_vx3` is true) take their model from one resolver that reads `ANTHROPIC_SMALL_FAST_MODEL` first; unset, a gateway host runs them on the main chat's model. `helperModel` carries the variable through every door, defaulting to the full id `claude-haiku-5-5`, and the hygiene unset stays so an exported value never reaches a launch the settings do not carry. `ANTHROPIC_DEFAULT_HAIKU_MODEL` is not used: it redefines the `haiku` alias, so it would move Haiku sub-agents too. A caller's own `--settings` on a headless run replaces pfm's whole payload, this key included.
- **Agent teams are off, always.** `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0` is a constant row, not a config key, and no project file needs to carry it.
- **The plugin env rides the launch.** `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` and the auto-compact window are launch values, never account `settings.json` keys; keys an older install wrote there stay, outranked by `--settings`. The variable does not override a cached `cachedGrowthBookFeatures.tengu_plugin_hooks_modules: false` in `{configDir}/.claude.json`, which a gateway host never refreshes; `pfm doctor`'s `function-hook-modules` row reports it.

## How Claude layers its settings

Claude Code's own precedence, highest first: managed settings (`/etc/claude-code/` on Linux, `/Library/Application Support/ClaudeCode/` on macOS) → command line (`--settings`, flags) → `.claude/settings.local.json` → `.claude/settings.json` → the account's `settings.json`. A settings `env` entry overwrites the same variable exported in the shell. A flag beats its settings key (`--model` over `model`). Hooks merge across every layer rather than replacing each other.

pfm supplies launch values at two layers: managed settings for retention through every launch path, and the command line for the other launch values. Claude's plugin commands also write shared user plugin state through the primary account ([claude-config-dir.md](claude-config-dir.md#what-pfm-does-not-write)).

## The registry

```go
// pfm/internal/claudelaunch/knobs.go
type Knob struct {
    Name      string // the config key, or the constant's name
    Wire      Wire   // how it reaches Claude
    Target    string // settings path, env name or flag
    Source    Source // where its value comes from
    Default   any
    Reason    string // one line: why pfm sets it
}
```

`Knobs` lists every row. `Render(Request, pfmconfig.Config) (Launch, error)` resolves each row for one launch and returns `Launch{Unset, Env, Wrapper, Binary, Argv}`. `ClaudeSpawn.ShellCommand` (`pfm/internal/action/claude_spawn.go`) prints it as `env -u … NAME=value … [wrapper] claude <argv>` for a tmux pane; `ClaudeSpawn.Command` execs it directly, the wrapper first when there is one. Neither adds a word of its own. The wrapper is the cost ledger's launcher ([cost-ledger.md](cost-ledger.md#the-launch)); it execs Claude and keeps its pid, so the process a live chat shows is Claude's own.

## Wires

| Wire | Reaches Claude as | Rows |
| --- | --- | --- |
| `WireSettings` | a key in the single `--settings` JSON (`env.*`, `hooks`, `statusLine`, …) | most |
| `WireEnv` | a process environment assignment | `CLAUDE_CONFIG_DIR`, the cache handoff `CACHE_LIVE_CONTROL_MAIN_TTL`, `CLAUDE_CODE_SHELL` |
| `WireUnset` | `env -u NAME` before exec | the hygiene list |
| `WireFlag` | a command-line flag | the prompt file, MCP config, autonomy pair, model, effort, session verbs |
| `WireWrapper` | argv words in front of the binary (`Launch.Wrapper`) | `costLedger` |
| `WireWrapperEnv` | a process environment value only the wrapper can set, because it depends on a socket the wrapper binds just before exec | the nine exporter variables, the `PFM_COST_LEDGER` marker and `PFM_COST_LEDGER_PID` |

## Knobs

| Knob | Wire → target | Source | Default |
| --- | --- | --- | --- |
| `configDir` | env `CLAUDE_CONFIG_DIR` | account | the account's own `configDir`, `~/.cc/{id}` by default; set on every account launch |
| hygiene | unset `CLAUDE_CODE_SESSION_ID`, `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CONFIG_DIR`, `CLAUDE_PROJECT_DIR`, `ENABLE_PROMPT_CACHING_1H`, `FORCE_PROMPT_CACHING_5M`, `CLAUDE_CODE_PROMPT_CACHE_TTL`, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL`, `CACHE_LIVE_CONTROL_MAIN_TTL`, `CACHE_LIVE_CONTROL_AGENTS_TTL`, `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK`, `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY`, `CODEX_THREAD_ID` | constant | — |
| `cache1h` | process env `CACHE_LIVE_CONTROL_MAIN_TTL=1h` or `5m` beside `CLAUDE_CONFIG_DIR`, never the settings `env` block: the main chat's starting TTL handed to the cache-live-control plugin, which owns every TTL from then on; pfm sets no Claude Code TTL variable (the handoff paragraph below); a headless run keeps it in its settings `env` | launch → config | `true` |
| `shell` | env `CLAUDE_CODE_SHELL={absolute bash}` in the launch's process environment, so the Bash tool of the chat and, by inheritance, of its sub-agents runs under bash, not the login shell (zsh does not word-split an unquoted `$VAR` the bash idioms agents write rely on); every door carries it: `Render`, the passthrough launcher (`SessionEnv`), a headless run's environment. An inherited value Claude Code would accept (absolute, executable, its path naming bash or zsh) is kept and nothing is added; a refused one is warned and replaced. No bash on `PATH`: nothing is added, the launch goes ahead, the activity log warns, `pfm config claude` shows the reason and `pfm doctor` fails its required `bash` | host | `bash` on `PATH` |
| `systemPrompt` | `professor`: flag `--system-prompt-file`; `lean`: settings `env.CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1`; `production`: nothing | config | `production` |
| `nativeCursor` | settings `env.CLAUDE_CODE_NATIVE_CURSOR=1` | config | `false` |
| `maxSubagentSpawnDepth` | settings `env.CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` | config | `8` |
| `maxConcurrentSubagents` | settings `env.CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` | config | unset (Claude's own default) |
| `webSearchesPerSession` | settings `env.CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION` | config | `9007199254740991` |
| `autoCompactWindow` | settings `env.CLAUDE_CODE_AUTO_COMPACT_WINDOW` | config | `100000` |
| `helperModel` | settings `env.ANTHROPIC_SMALL_FAST_MODEL`: the model of Claude Code's helper calls (WebFetch's page summary, the WebSearch call, session and tool-use-summary titles); the main chat and the sub-agents keep their own models. Every door carries it: `Render`, the passthrough launcher (`SessionEnv`, as a process-environment value replacing an inherited one), a headless run's `--settings`, where it is the default or `helperModel` from `--pfm-settings`, since headless reads no config ([claude-headless.md](claude-headless.md#decisions)) | config | `claude-haiku-5-5` |
| `tmuxTruecolor` | settings `env.CLAUDE_CODE_TMUX_TRUECOLOR=1` | config | `true` |
| `noFlicker` | settings `env.CLAUDE_CODE_NO_FLICKER=1`, keeping the fullscreen renderer past Claude's boot canary; not on a query | account settings `tui: fullscreen` | off |
| agent teams | settings `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0` | constant | — |
| function hooks | settings `env.CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` | constant | — |
| output style | settings `outputStyle: "default"` | constant | — |
| `theme` | settings `theme` | config | unset |
| `cleanupPeriodDays` | settings `cleanupPeriodDays` | config | `36500` |
| hooks | settings `hooks` — the pfm hook set, `docs/design/hooks/hooks.md` | constant | — |
| status lines | settings `statusLine`, `subagentStatusLine` — `docs/design/context/statusline.md` | constant | — |
| `costLedger` | wrapper `pfm internal cost-launch --` in front of the binary: it binds a loopback sink and execs Claude with the exporter variables below; `false`, or the user's own telemetry settings, launch without and record `PFM_COST_LEDGER=off` or `preempted` — [cost-ledger.md](cost-ledger.md#the-launch) | config (machine-wide) | `true` |
| cost exporters | wrapper env `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_LOGS_EXPORTER=otlp`, `OTEL_TRACES_EXPORTER=otlp`, `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`, `OTEL_METRICS_EXPORTER=none`, `OTEL_EXPORTER_OTLP_PROTOCOL=http/json`, `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:{port}`, `OTEL_LOGS_EXPORT_INTERVAL=1000`, `OTEL_BSP_SCHEDULE_DELAY=1000` | constant | — |
| cost marker | wrapper env `PFM_COST_LEDGER=on\|off\|preempted\|failed`, the decision the status lines read | constant | — |
| cost pid | wrapper env `PFM_COST_LEDGER_PID={pid}`, the process the armed sink serves: a second pass of that same process (the wrapper exec'd pfm's `claude` shim) keeps its sink | constant | — |
| MCP | flag `--mcp-config {file}` — a per-launch 0600 file holding enabled `chat-professor` / `harvester-professor` and third-party entries | config | — |
| `permissionMode` | flags `--allow-dangerously-skip-permissions --dangerously-skip-permissions` on `bypass` | config | `bypass` |
| model | flag `--model` | launch | unset |
| effort | flag `--effort` (`low\|medium\|high\|xhigh\|max`) | launch | unset |
| session id | flag `--session-id {uuid}` on a fresh chat — pfm assigns the id so the launch record exists before Claude starts | door | — |
| session verbs | flags `--resume`, `--fork-session`, `--name` | door | — |

`--mcp-config` names a per-launch file carrying every `mcp.thirdParty` entry and, when enabled, separate `chat-professor` and `harvester-professor` entries with family-scoped stdio commands: `{type: stdio, command: ~/.local/bin/pfm, args: [mcp, serve, --stdio, --family, chat|harvester]}`. When both families are off, third-party entries still ride the file; with no entries neither file nor flag exists. The payload never sits on argv, where `ps` and `/proc/{pid}/cmdline` would show a third-party `env` value or `Authorization` header to every local user: Render writes `{"mcpServers":{…}}` atomically (scratch file, then rename) to `~/.local/state/pfm/mcp-config/claude-mcp-{random}.json`, mode 0600 in a 0700 directory it refuses when that path is a symlink, and passes the path. Claude reads the file once at startup; each write first prunes the directory's launch files older than seven days. Separately, install inspects existing account `.claude.json` files found through configured accounts, the account roster, ambient config and default paths. It removes exact pfm-owned legacy entries, emits or reconciles enabled family entries, preserves and reports foreign `professor` entries, and leaves absent registry files uncreated. Doctor uses the same inspector and reports account state, foreign entries and unreadable files. Config validation reserves the names `chat-professor` and `harvester-professor` and requires each third-party value to be a JSON object.

The interactive cache handoff stays in process environment beside `CLAUDE_CONFIG_DIR`, outside the settings `env` block. Claude re-applies that block on a settings reload; putting the handoff there would hand the plugin a value it already consumed. The cache-live-control plugin owns subsequent main and sub-agent TTL changes. pfm sets no Claude TTL variable and never `CACHE_LIVE_CONTROL_AGENTS_TTL`; hygiene clears inherited values. Headless keeps the handoff in its settings `env`.

### CheckConfigDir

Before rendering an account launch, `CheckConfigDir` (`pfm/internal/claudelaunch/render.go`) requires that account's directory to exist and never creates it. It classifies the directory with `InspectConfigDir` (`pfm/internal/claudelaunch/configdir.go`). Install's store wiring, the `account-is-store` and `account-entry-real` host checks and doctor's account rows use the same predicate, so a directory launch refuses is one doctor names. A symlinked directory is followed: it launches when its resolved real path is a directory that is neither the store nor inside it, and `CLAUDE_CONFIG_DIR` keeps the literal path. Refusals:

- Missing: `account {id}: {config dir} does not exist — run pfm install`.
- Not a directory: `account {id}: {config dir} is not a real directory — run pfm doctor`.
- Resolves to the store or into it: `account {id}: {config dir} resolves to the Claude store — run pfm doctor`.
- Inspection error, including a dangling link: `account {id}: inspect {config dir}: {cause}`.

`Render` refuses an unknown nonzero account in a configured roster with `account {id} is not in the configured roster`. `CLAUDE_CONFIG_DIR` is set for every configured account, including account 1. An explicit directory-only request receives the same check without the account prefix. Headless account requests also call `CheckConfigDir`.

## Sources

- **config** — `pfm.config.json`'s `claude` block, overridden by the launching account's own `claude` block.
- **machine config** — the `mcp` block's enabled families decide whether `--mcp-config` carries `professor`; `mcp.thirdParty` supplies every other entry. The HTTP port plays no part in pfm's stdio entry.
- **launch → config** — a choice made for this one launch (the picker's cache toggle, `pfm chat new --cache 1h|5m`, `pfm chat reload --cache`); absent a choice, the config value.
- **launch** — only a per-launch choice; unset means the flag is omitted.
- **constant** — fixed in the registry; changing it is a code change.
- **host** — the launch's own environment, else the host's `PATH` (`shell`): `pfm config claude` shows `source=env` for a kept inherited value, `source=PATH` otherwise.
- **account / door** — the chosen account's roster entry, or for `noFlicker` its `settings.json` (`WantsFullscreen`: absent file off; an unreadable one logged and launched without the knob, and shown as its error by `pfm config`); the verb that launched.

No inherited environment variable decides a value but `CLAUDE_CODE_SHELL` (the `shell` row): the unset list clears them first.

## The launch record

pfm remembers what it launched in `pfm.db`, table `launch` — one row per session: `session_id`, `engine`, `account`, `cache1h`, `launched_at`, `updated_at`. It is the source every later reader uses; nothing reads a launch value back out of a running process's environment.

- **One writer.** `fleetdb.RecordLaunch(ctx, values, launch, at)` inserts or updates a row. Every door calls it before exec: a fresh chat with the id it passes as `--session-id`; a resume or reload with the resumed id, updating `account` and `cache1h`; a fork once its new session id resolves.
- **One reader.** `(*fleetdb.Launches).LaunchFor(ctx, sessionID)` returns the row, `ErrNoLaunch` for a session pfm never launched, or the read error.
- **Readers:** the picker's ⚡1h badge; `pfm chat reload`, which carries the chat's account and cache into the respawn unless overridden; the statusline's cache window, keyed by the `session_id` in the statusline payload; account attribution of rows that are not live (compose), which is how a transcript in the shared store is tied to the account it ran on.
- **No record** — a chat pfm did not launch — shows no badge and no medal. **A failed read** renders as an error marker (`⚠`), never as a 5-minute cache or a missing account.
- Spawn-audit does not read the record: it audits what actually runs, through `claudelaunch.Parse` over the live argv with the binary first.

## Config keys

The `claude` block of `pfm.config.json`; each key also takes a per-account override.

| Key | Default | Knob |
| --- | --- | --- |
| `binary` | `claude` | the real binary behind the launcher |
| `permissionMode` | `bypass` | autonomy flags (`prompted` omits them) |
| `systemPrompt` | `production` | `production` / `lean` / `professor` |
| `cache1h` | `true` | the cache-live-control handoff (`CACHE_LIVE_CONTROL_MAIN_TTL`) in the process environment |
| `theme` | unset | `theme` |
| `nativeCursor` | `false` | native cursor |
| `maxSubagentSpawnDepth` | `8` | sub-agent depth |
| `maxConcurrentSubagents` | unset | sub-agent concurrency |
| `webSearchesPerSession` | `9007199254740991` | web-search cap |
| `autoCompactWindow` | `100000` | auto-compact window |
| `helperModel` | `claude-haiku-5-5` | the helper-call model (`ANTHROPIC_SMALL_FAST_MODEL`); a full model id or alias with no whitespace, an empty value is refused; an account may override it |
| `tmuxTruecolor` | `true` | truecolor under tmux |
| `noFlicker` | off | not a config key: on when the account's `settings.json` has top-level `"tui": "fullscreen"` |
| `costLedger` | `true` | the per-call cost ledger ([cost-ledger.md](cost-ledger.md)); machine-wide, an account-level value is refused |
| `cleanupPeriodDays` | `36500` | transcript retention (also the managed value) |
| `requireManagedCleanup` | `true` | `pfm install` writes the managed drop-in and `pfm doctor` warns when it is absent or lacks `cleanupPeriodDays` or `prependPlugins`; `false` turns off both, the drop-in's `prependPlugins` included |

## The rendered launch

```text
env -u {hygiene…} CLAUDE_CONFIG_DIR={config dir} CACHE_LIVE_CONTROL_MAIN_TTL={1h|5m} [PFM_COST_LEDGER=off|failed] \
  [~/.local/bin/pfm internal cost-launch --] {binary} {door verbs} \
  --settings '{"outputStyle":"default","cleanupPeriodDays":36500,"env":{…},"hooks":{…},"statusLine":{…},"subagentStatusLine":{…}[,"theme":…]}' \
  [--mcp-config ~/.local/state/pfm/mcp-config/claude-mcp-{random}.json] \
  [--model M] [--effort E] [--system-prompt-file F] [--allow-dangerously-skip-permissions --dangerously-skip-permissions]
```

## The system prompt

- **The composed file is built, not staged.** `make -C pfm prompts` (run by `dev.sh build pfm` and by `/pfm:release`) composes `share/head.md` + `{engine}/professor.md` + `share/tail.md` into the tracked `pfm/harness-prompts/composed/{claude,codex,opencode}.md`. `dev.sh test pfm` fails when a composed file differs from its parts.
- **Launch points at the repo.** `--system-prompt-file` is `{clone}/pfm/harness-prompts/composed/claude.md`, `{clone}` from the source-repo record. Nothing is copied at install.
- **Workbench launches** replace the composed file with the manifest prompt; explicit `--system-prompt-file` / `--harness-prompt` win, and a role seat composes on that base (`internal/workbench/persona.go`, `internal/agentrole/base_prompt.go`).
- **Role seats** compose the claude prompt with the role into a seat file under the SID dir and pass that instead (`agentrole.ComposeSeatPrompt`); `pfm chat reload` refreshes it before respawning.
- **Missing file fails open, loudly:** the launch omits the flag and starts on Claude's own prompt, and the starter is told why (`claudelaunch.PromptWarning`, the one verdict) on the surface they launched from: stderr for `chat new` / `chat branch` / the picker / the launcher, the tool message for `open`, the pane for agent resume, the reload worker's log plus a pane display for reload; spawn-audit reports it too. A surface that attaches next (the picker, the launcher, `chat new --attach`) holds for Enter when stdin and stderr are both a terminal, so the attach cannot cover the warning (`internal/launchnotice`); off a terminal it only prints, and Ctrl-C at the launcher's prompt kills the server it made. Agent resume prints in the pane without waiting, since a detached pane has nobody to press Enter. A caller-stated `--system-prompt` or `--system-prompt-file` is deliberate and never warned.

## Doors

Each door builds a `claudelaunch.Request` — account, purpose, door verbs, per-launch choices — and calls `Render`.

A launch that resumes a session in place (`Resume`, or `--resume`, `-r`, `--continue`, `-c` in its args, with no `--fork-session`) carries no `--name`: the session restores its own name from its transcript, and Claude applies a launch name only once its session registers, so one landing then would revert a `/rename` the user typed meanwhile. A fresh launch and a fork keep theirs.

### PlanClaude

`pfm chat new` and MCP `new`. Verbs: `--session-id`, `--name`, the prompt positional. Per-launch: `--account`, `--cache 1h|5m`, `--model`, `--effort`, role seat file.

### HeadlessFork

`pfm chat branch`. Verbs: `--resume {id} --fork-session --name {name}`; model from the running chat. Its account is `--account`, else the parent's launch record, else the account its live seat runs on; with none of them and more than one configured account it refuses and asks for `--account`. It records the launch once the fork's new session id resolves. Claude writes the fork's transcript at its first prompt, so until then the fork is listed by its crumb under the window name it was launched with.

### Synthesize

The picker and `pfm chat open` / MCP `open`: `NewClaude` (`--session-id`, prompt), `ResumeClaude` (`--resume {id}`), `Agent` (`pfm internal agent-open`). Per-launch: the picker's account and cache toggle, whose initial state is the config value.

### AgentOpen

`pfm internal agent-open` inside the pane: `--resume {id}`, direct exec.

### claudeRun

`pfm chat reload` respawns the pane in place: `--resume {id}`, `--model`/`--effort` when given, the refreshed seat file. Account and cache come from `--account`/`--cache` when given, else from the chat's [launch record](#the-launch-record); the respawn updates the record.

### LauncherRun

A `claude` typed at a shell, through [the managed launcher](#the-managed-launcher). Values from config; never the autonomy flags. A fresh launch gets an assigned `--session-id` and a launch record; `--resume {id}` records that id; a launch whose session id pfm cannot know (`--continue`) records nothing.

### PurposeQuery

`claude agents --json` for the reaper, inject-resume and agent-open listings: the `--settings` payload, hygiene and account environment; no session-start flags.

### The Herdr seat door

A Claude chat born in a Herdr pane can be a **Herdr seat**: the engine runs in the pane's own terminal under Herdr, with no tmux server between them, so Herdr shows its state natively. The door is native Claude only. Under a Herdr backend, `pfm chat new` for Codex or OpenCode is refused before anything is born with exit 2, naming the engine, `native Herdr raw launch path is not supported until phase 2` and `use --backend tmux` (`pfm/cmd/pfm/chat_new_command.go`; `spawn.reserveHerdr` in `pfm/internal/spawn/herdr.go` refuses the same engines for the MCP and picker doors). Backend selection itself is unchanged.

- **Backend choice.** `spawn.ChooseBackend` (`pfm/internal/spawn/backend.go`) takes an explicit `--backend auto|tmux|herdr`, else `chat.backend` ([pfm-home.md § chat](pfm-home.md#chat)), else `auto`. `auto` picks Herdr only when `HERDR_ENV=1`, `HERDR_SOCKET_PATH` and `HERDR_PANE_ID` are set, `TMUX` is unset and a `pane.get` probe reaches the pane; `TMUX` set wins and means tmux. An unreachable pane under `auto` prints one warning and starts in tmux. A forced `herdr` that cannot reach its pane exits 1 and creates no fallback chat. A requester that is itself a seat sets the socket, pane and workspace in place of the process environment (a Herdr requester) or keeps `auto` on tmux (a tmux requester).
- **Doors that choose it.** The managed launcher ([LauncherRun](#launcherrun)) reaches `pfm internal launch`, whose `--backend` defaults to `chat.backend` and feeds `launchBackend` (`pfm/internal/hookentry/launch_native.go`): on `herdr` it renders the same typed `claudelaunch` recipe and does not build the tmux server wrapper, and it refuses a pane that already has a live seat row (`SeatByPane`). `pfm chat new` parses `--backend` (`pfm/cmd/pfm/chat_new_command.go`).
- **Recipe and seat first.** `launchNative` writes the recipe, then the seat row, then runs the host. The recipe is a private (0600), atomic JSON file `seats/{seatid}.json` beside `pfm.db` holding `Binary`, `Args` (without argv0), `Env`, `CWD` and `cost_ledger` (`SeatRun.CostLedger`, the producer's account-effective cost-ledger choice; a legacy or hand-written recipe without the field means disabled); the seat id passes `^[a-zA-Z0-9_-]+$`. The seat row (`fleetdb.Seat`, table `seat`, `pfm/internal/fleetdb/seat.go` and `migration_v6.sql`) is addressed by backend: `tmux_socket`/`tmux_pane` or `herdr_socket`/`herdr_pane`/`herdr_workspace`, plus `session_id`, `name` and `state` (`live`, `lost`, `failed`). A store failure reads as an error, never as a missing seat. `PutSeat` and `UpdateSeat` each admit at most one `state='live'` Herdr row per socket and pane in one guarded SQL statement, for the launcher, the picker's `PrepareHerdr` reservation and a pane bind alike; a second live owner is refused, and a stale live owner blocks the pane (nothing supersedes or recovers it). `SeatByPane` returns the error `herdr pane … has multiple live seats` for historical duplicate rows rather than choosing one. The account launch registry stays the only producer of the recipe; nothing parses a shell command line.
- **The host.** `pfm internal seat-host {seatid}` (`pfm/internal/hookentry/seathost.go`) starts the engine from the recipe with argv0 taken from the registry descriptor (the executable may be a version path), in its own process group that is the terminal's foreground group. `/dev/tty` serves only as an ioctl handle to resolve the pane's concrete terminal device (`seatTerminalPath`); the engine's stdin, stdout and stderr (fds 0-2) are that concrete device, not `/dev/tty`. Immediately before starting the engine it arms the cost ledger from the recipe's `cost_ledger` with its own (the supervisor's) process, never the requester that planned the birth; no shell wrapper or creator-bound sink is involved. It forwards `HUP`, `INT`, `TERM` and `QUIT` to that group. When the engine ends normally the host removes the seat row and recipe and the pane closes. A start that cannot happen (a missing or malformed recipe, a missing executable) leaves the seat `failed` with the cause in `LostNote`, the recipe kept where one exists.
- **The reporter.** `pfm internal herdr-state` (`pfm/internal/hookentry/herdr_seat.go`) handles a native seat before the tmux bridge: the event must match the seat's socket and pane. `SessionStart` stores the session id and sends `pane.report_agent` with `resume_argv` `[pfm, chat, open, --restored, {sid}]`; native attach, detach and sync events are ignored. The tmux bridge is unchanged. The lookup is read-only (`fleetdb.OpenSharedStateReadOnly`); a writable store opens only for `SessionStart`, `SessionEnd` or a changed session id, and a state-only hook (working, idle, blocked) writes no durable `UpdatedAt`. A seat with an unreadable or newer store is a visible error and the reporter stays fail-open. For a tmux-launched seat, `herdr-state` runs its durable `SessionStart` and `SessionEnd` bookkeeping after the unchanged tmux bridge, so a durable read or write failure is joined into the error but never suppresses or consumes the bridge's report. The `--restored` flag itself is a phase-2 recovery path and is not a working launch behavior yet.
- **The socket facade.** `herdr.SeatRequest` (`pfm/internal/herdr/seat.go`) is the JSON socket call; only `agent.wait` takes a caller-bounded deadline longer than the ordinary reporter timeout.
- **`--then`.** On both backends `--then` keeps its ordered same-target steers. Pfm sends no automatic transcript reply: an answer comes back as an explicit, ordinary call in the other direction.
- **Birth from the CLI, MCP and picker.** `chat.BirthBackend` and `chat.BirthHerdr` (`pfm/internal/chat/birth.go`) keep a native birth out of the tmux-only tail of `pfm chat new`: they record the launch, then call `spawn.SpawnHerdr` (`pfm/internal/spawn/herdr.go`), which reserves the seat row and recipe through `spawn.reserveHerdr`, then asks Herdr for the tab with `layout.apply`: a Herdr refusal deletes the reservation, while a transport, decode or response error keeps it because the tab may exist. `spawn.PrepareHerdr` is the picker's reservation only; it performs no tab cleanup. The recipe is the typed `fleetdb.SeatRun` from `ClaudeSpawn.SeatRun` (`pfm/internal/action/claude_spawn.go`) or `HeadlessPlan.SeatRun`, built from the launch registry and never from a shell line. `ClaudeSpawn.SeatRun` emits the registry's `launch.Binary`, `Argv`, environment and `CWD` with `cost_ledger` from `Machine.EffectiveClaude(Account).CostLedger`, with no managed shim or cost wrapper in it. `spawn.reserveHerdr` resolves the actual engine executable through `gather.ResolveClaudeBinary` (`pfm/internal/gather/claudebinary.go`; `installer.ResolveClaudeBinary` is its facade) before the seat row, recipe or layout exists, so tmux planning stays engine-independent. The summary says the chat is starting, not delivered. `--await` and `--attach` on a native birth are refused before anything is born (exit 2), so the new tab stays unfocused. The picker prepares the same pane's seat host. Only the backend choice is request-local: the MCP `new` path binds `spawn.WithBackendEnvironment(ctx, nil)` (`pfm/internal/mcpserv/actions.go`), so `spawn.BackendEnvironment` never falls back to the daemon's environment and the socket, pane and workspace come from the requester seat's metadata (`chat.ScopedSelf`); an MCP proxy requester carries its own backend. The recipe is not request-local: `ClaudeSpawn.SeatRun` builds `Env` from `launchEnvironment(os.Environ(), launch)` and `spawn.reserveHerdr` resolves the binary over the process `PATH`, so in the MCP daemon both read the daemon's ambient environment, as the tmux recipe in `ClaudeSpawn.Command` does.
- **Identity, listing, status, name.** `pfm chat whoami` resolves a Herdr seat only from the live seat row at the caller's `HERDR_SOCKET_PATH` and `HERDR_PANE_ID` (`fleetdb.Store.SeatByPane`: `state='live'`; several matching rows are an error, not a pick) when a pid in the caller's process ancestry is one of the `foreground_processes` that `pane.process_info` returns for that pane on the seat's socket (`pfm/internal/resolve/herdr.go`); the seat row stores no engine pid, and inherited `HERDR_*` variables alone confer no identity. Otherwise it prints `ERROR: this chat is not inside tmux ($TMUX unset, and no ancestor carries it) nor a pfm Herdr seat`. Discovery (`gather.ProbeHerdrSeats`) lists each socket's panes once per invocation for all its live seats: a transport failure is a `HERDR UNREACHABLE` row and an answered refusal such as a missing pane is a `HERDR ERROR` row, both keeping `pfm chat ls` nonzero; a `failed` or `lost` seat stays visible as a non-addressable diagnostic without failing the listing; a durable-store read failure is appended as an error while the fleet and live tmux rows survive; a transcript row is preserved, and every seat of one session keeps its own socket. Status prefers the transcript and falls back to `agent.get` for a native seat. `pfm chat name` types `/rename`, sets the pane label, a unique agent name and the durable seat name, converging without a tmux window.
- **Native lifecycle.** `kill`, `end`, `deactivate`, `reboot` and `reload` on a native seat are refused in phase 1 with `native Herdr {verb} is not supported until phase 2` before any destructive effect; only `reload` pins a refusal before any tmux probe, worker launch or log creation, and the other verbs make no such ordering promise (`pfm/cmd/pfm/chat_command.go`, `pfm/cmd/pfm/chat_reload_command.go`, `pfm/internal/chat/kill_server.go`, `pfm/internal/picker/picker.go`, `pfm/internal/ui/model.go`); the row stays visible. Tmux behavior is unchanged.
- **Call and wait.** A call to a native Claude seat refuses a busy or blocked target (`ABORT: target pane is busy; nothing was typed`, `... is blocked; nothing was typed`), otherwise sends `agent.prompt` once and reports `agent_prompted`; an unconfirmed send is `typed_unconfirmed`; a composer that holds a draft, a menu or the trust dialog is `undelivered` with nothing typed (`pfm/internal/call/herdr.go`). `--then` waits with `agent.wait` under a caller-set deadline and honors the producer's `until` states; a turn that goes working to idle between the stale-idle reply and the second until-working request can be missed (a known low residual, not a claimed success). All four directions (tmux or Herdr to tmux or Herdr) run the same call path; a reply is an explicit, ordinary second call, never an automatic callback. Raw Codex and OpenCode panes are refused (`raw-engine delivery is not implemented`).

### Every door that starts a Claude session

1. Render doors: `PlanClaude`, `HeadlessFork`, `Synthesize` (fresh, resume and agent fallback), `AgentOpen` resume, `claudeRun` and `LauncherRun` call `claudelaunch.Render`.
2. Headless: `claudelaunch.RenderHeadless` supplies `--settings` for `pfm headless exec`, `pfm ask`, the limits ACK and the doctor capture.
3. Passthrough that starts a session: `pfm internal launch` supplies the two plugin values in its process environment and, for an interactive start, arms the cost ledger itself (`costsink.PrepareCurrent`: the exporter variables and the `PFM_COST_LEDGER` marker, [cost-ledger.md](cost-ledger.md#doors)).
4. VS Code terminal: its `PATH` finds pfm's `claude` shim, so a typed session takes a render or passthrough door.

Query-purpose `agents --json` and the exempt subcommands start no session.

### Doors pfm does not own

The VS Code Claude extension's panel is not a pfm launch door. pfm never launches it. With `pfm install --yes --vscode`, pfm writes `claudeCode.environmentVariables` in owned VS Code settings to supply the primary account's `CLAUDE_CONFIG_DIR`. The panel reads the shared files through that account's links and does not receive pfm's rendered flags.

## The managed launcher

1. `~/.local/bin/claude` links to `~/.local/share/pfm/install/bin/claude`, which runs `pfm internal claude-launch "$@"`, first giving an unset `CLAUDE_CONFIG_DIR` the login default ([claude-config-dir.md](claude-config-dir.md#launches-outside-pfm)).
2. `claude-launch` resolves the real binary — `claude.binary`, else the newest native version under `~/.local/share/claude/versions/`, else `PATH` — never the launcher itself.
3. **Passthrough** — `PFM_LAUNCH_PASSTHROUGH=1`, already inside a pfm tmux socket, `-p`/`--print`/`--output-format`/`-h`/`--help`/`--version`/`-v`, or the subcommands `agents mcp update install doctor setup-token plugin config` — execs the real binary. Such a run carries no pfm settings except, when it starts a session, the two plugin env values in its process environment and, unless it is a print-mode run, the cost ledger's exporter variables and marker; the managed `cleanupPeriodDays` still applies to it.
4. Otherwise it renders `LauncherRun` into a fresh `cc-` tmux socket.

The `SessionStart` hook `launcher-repair` re-links a displaced launcher. Version retention keeps the newest two versions plus any a live process runs.

## The zsh shim

`~/.zshrc` sources `{clone}/pfm/internal/installer/assets/shim/pfm.zsh` directly; the file is static — nothing is rendered into it at install. It exports `CLAUDE_DISABLE_ADOPT=1`, `CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF=1`, `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=2` into the interactive shell, and defines `claude()`, which calls `~/.local/bin/claude` and closes the terminal after a TUI session, `codex()`, which runs `command codex` with the transport `pfm internal codex-transport` answers (`--no-daemon` for a TUI; [Codex pane registry](../hooks/hooks.md#herdr-reporter)) and closes the terminal the same way, and `cx()`, which hands off to `pfm internal codex-launch`. The Codex launch reads each account's yolo setting from `pfm.config.json` and strips the registry's hygiene rows in Go; the shim carries no env list of its own.

## Long launch lines

A pane run over 8 KiB goes through a one-shot `/bin/sh` script under the tmux dir that deletes itself and `exec`s the run (`pfm/internal/tmux/launch.go`). The `--settings` payload with hooks is the common reason a run crosses the budget.

## pfm config claude

`pfm config claude [--account N]` prints every knob for that account — knob, wire, target, resolved value, and the source that won (`config`, `account`, `default`, `constant`). It is the answer to "what does a chat start with, and where do I change it".

## pfm doctor spawn-audit

Reads each live `cc-` chat's Claude argv from `/proc`, decodes it with `claudelaunch.Parse` — the inverse of `Render`, and the only parser of a Claude launch line — and classifies it:

| Verdict | Meaning |
| --- | --- |
| `INJECTED` | argv carries `--settings` with `outputStyle:"default"` and the registry's hook set, plus the prompt material the account's `systemPrompt` names (`--system-prompt-file`, or `env.CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT` inside the `--settings` payload) |
| `VIOLATION` | a fresh launch without the registry payload, or professor prompt material missing because the composed prompt is unavailable; that reason names the resolve error and blames no spawn site |
| `PREDATES-LAYER` | the process started before the current spawn door, with its age named, or its hook set differs from the registry at any age; reload to carry it |
| `ROLE-OK` / `ROLE-MISMATCH` / `ROLE-CHECK-FAILED` | a seat prompt file against its role |

Account attribution matches the process environment's `CLAUDE_CONFIG_DIR` to a roster entry by physical path, falling back to the cleaned spelling when either path cannot be resolved, before grading against that account's effective settings. A trailing slash or a symlinked spelling still matches. An empty roster grades against the primary account with no note. A missing or unmatched value is graded against the primary account with `account unmatched; graded against primary`. An unreadable environment reports `account environment unreadable; graded against primary`. No account is attributed to the store.

Values set through the `--settings` `env` block are read from argv, never from `/proc/{pid}/environ`: Claude writes them into its environment after exec. A probe that cannot run reports `CHECK FAILED to run … — live chats unaudited`, never "no chats".

## Surfaces that stay in sync

- `claudelaunch.Knobs` ↔ `example.pfm.config.json` (every config-sourced knob has its key there) ↔ `pfm config claude` ↔ the mock engine's accepted flags (`pfm/internal/mockengine/claude.go`).
- The hygiene rows ↔ the Codex launch's unset list (`pfm internal codex-launch`) ↔ the headless strip lists ↔ the doctor harness probe's capture environment — all read from the registry.
- `outputStyle` constant ↔ the gate in `infra/check-self-hosted-manifest.sh`.
- The cost rows (`costLedger`, the exporter variables, the `PFM_COST_LEDGER` marker) ↔ `pfm/internal/claudelaunch/cost.go` ↔ [cost-ledger.md](cost-ledger.md); `PFM_COST_LEDGER_DIR` (`pfm/internal/paths/paths.go`) names the ledger directory the sink writes and every reader opens.
- `pfm/harness-prompts/composed/*.md` ↔ their parts.
- `chat.backend` (`pfm/internal/config/chat.go`) ↔ `example.pfm.config.json` ↔ [pfm-home.md § chat](pfm-home.md#chat) ↔ `spawn.ChooseBackend`.
