# Claude interactive launch

Every Claude Code chat pfm starts in a tmux pane is described by one table, the launch registry `claudelaunch.Knobs` (`pfm/internal/claudelaunch/knobs.go`), and rendered by one function, `claudelaunch.Render` (`pfm/internal/claudelaunch/render.go`). Every flag, environment variable and settings key pfm hands Claude at launch is a row there; nothing else in pfm spells one. This file holds the registry's shape, where each value comes from, how it reaches Claude, the code paths that launch a chat, the managed `claude` launcher, and the doctor audit. Headless runs are in [claude-headless.md](claude-headless.md); files pfm places on disk are in [claude-config-dir.md](claude-config-dir.md).

## Contents

- [Decisions](#decisions)
- [How Claude layers its settings](#how-claude-layers-its-settings)
- [The registry](#the-registry)
- [Wires](#wires)
- [Knobs](#knobs)
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
- **Agent teams are off, always.** `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0` is a constant row, not a config key, and no project file needs to carry it.
- **The plugin env rides the launch.** `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` and the auto-compact window are launch values, never account `settings.json` keys; keys an older install wrote there stay, outranked by `--settings`.

## How Claude layers its settings

Claude Code's own precedence, highest first: managed settings (`/etc/claude-code/`) → command line (`--settings`, flags) → `.claude/settings.local.json` → `.claude/settings.json` → the account's `settings.json`. A settings `env` entry overwrites the same variable exported in the shell. A flag beats its settings key (`--model` over `model`). Hooks merge across every layer rather than replacing each other.

pfm writes at two layers only: managed settings for the one value that must survive any launch path ([claude-config-dir.md](claude-config-dir.md#managed-settings)), and the command line for everything else.

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

`Knobs` lists every row. `Render(Request, pfmconfig.Config) (Launch, error)` resolves each row for one launch and returns `Launch{Unset, Env, Argv}`. `ClaudeSpawn.ShellCommand` (`pfm/internal/action/claude_spawn.go`) prints it as `env -u … NAME=value … claude <argv>` for a tmux pane; `ClaudeSpawn.Command` execs it directly. Neither adds a word of its own.

## Wires

| Wire | Reaches Claude as | Rows |
| --- | --- | --- |
| `WireSettings` | a key in the single `--settings` JSON (`env.*`, `hooks`, `statusLine`, …) | most |
| `WireEnv` | a process environment assignment | `CLAUDE_CONFIG_DIR` only |
| `WireUnset` | `env -u NAME` before exec | the hygiene list |
| `WireFlag` | a command-line flag | the prompt file, MCP config, autonomy pair, model, effort, session verbs |

## Knobs

| Knob | Wire → target | Source | Default |
| --- | --- | --- | --- |
| `configDir` | env `CLAUDE_CONFIG_DIR` | account | the account's `configDir` (omitted for the implicit account) |
| hygiene | unset `CLAUDE_CODE_SESSION_ID`, `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CONFIG_DIR`, `CLAUDE_PROJECT_DIR`, `ENABLE_PROMPT_CACHING_1H`, `FORCE_PROMPT_CACHING_5M`, `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK`, `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY`, `CODEX_THREAD_ID` | constant | — |
| `cache1h` | settings `env.ENABLE_PROMPT_CACHING_1H=1`, or `env.FORCE_PROMPT_CACHING_5M=1`; also written to the [launch record](#the-launch-record) | launch → config | `true` |
| `systemPrompt` | `professor`: flag `--system-prompt-file`; `lean`: settings `env.CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1`; `production`: nothing | config | `production` |
| `nativeCursor` | settings `env.CLAUDE_CODE_NATIVE_CURSOR=1` | config | `false` |
| `maxSubagentSpawnDepth` | settings `env.CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` | config | `8` |
| `maxConcurrentSubagents` | settings `env.CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` | config | unset (Claude's own default) |
| `webSearchesPerSession` | settings `env.CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION` | config | `9007199254740991` |
| `autoCompactWindow` | settings `env.CLAUDE_CODE_AUTO_COMPACT_WINDOW` | config | `100000` |
| `tmuxTruecolor` | settings `env.CLAUDE_CODE_TMUX_TRUECOLOR=1` | config | `true` |
| agent teams | settings `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0` | constant | — |
| function hooks | settings `env.CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` | constant | — |
| output style | settings `outputStyle: "default"` | constant | — |
| `theme` | settings `theme` | config | unset |
| `cleanupPeriodDays` | settings `cleanupPeriodDays` | config | `36500` |
| hooks | settings `hooks` — the pfm hook set, `docs/design/hooks/hooks.md` | constant | — |
| status lines | settings `statusLine`, `subagentStatusLine` — `docs/design/context/statusline.md` | constant | — |
| MCP | flag `--mcp-config '{"mcpServers":{"professor":{…}}}'` — one `professor` entry, `{type: stdio, command: ~/.local/bin/pfm, args: [mcp, serve, --stdio]}` (the shape `pfm install` registers for Codex and OpenCode), present when `chat` or `harvester` is enabled in `mcp`, absent when both are off | config | — |
| `permissionMode` | flags `--allow-dangerously-skip-permissions --dangerously-skip-permissions` on `bypass` | config | `bypass` |
| model | flag `--model` | launch | unset |
| effort | flag `--effort` (`low\|medium\|high\|xhigh\|max`) | launch | unset |
| session id | flag `--session-id {uuid}` on a fresh chat — pfm assigns the id so the launch record exists before Claude starts | door | — |
| session verbs | flags `--resume`, `--fork-session`, `--name` | door | — |

`--mcp-config` adds to the account's own MCP servers; it never replaces them.

## Sources

- **config** — `pfm.config.json`'s `claude` block, overridden by the launching account's own `claude` block.
- **machine config** — the `mcp` block's enabled servers; they live outside `claude` and decide whether the `--mcp-config` row carries the `professor` entry. The HTTP port plays no part: the entry is stdio.
- **launch → config** — a choice made for this one launch (the picker's cache toggle, `pfm chat new --cache 1h|5m`, `pfm chat reload --cache`); absent a choice, the config value.
- **launch** — only a per-launch choice; unset means the flag is omitted.
- **constant** — fixed in the registry; changing it is a code change.
- **account / door** — the chosen account's roster entry; the verb that launched.

No inherited environment variable decides a value: the unset list clears them first.

## The launch record

pfm remembers what it launched in `pfm.db`, table `launch` — one row per session: `session_id`, `engine`, `account`, `cache1h`, `launched_at`, `updated_at`. It is the source every later reader uses; nothing reads a launch value back out of a running process's environment.

- **One writer.** `fleetdb.RecordLaunch(ctx, fleetdb.Launch{…})` is the only code that inserts or updates a row. Every door calls it before exec: a fresh chat with the id it passes as `--session-id`; a resume or reload with the resumed id, updating `account` and `cache1h`; a fork once its new session id resolves.
- **One reader.** `fleetdb.LaunchFor(ctx, sessionID)` returns the row, `ErrNoLaunch` for a session pfm never launched, or the read error.
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
| `cache1h` | `true` | the cache env pair |
| `theme` | unset | `theme` |
| `nativeCursor` | `false` | native cursor |
| `maxSubagentSpawnDepth` | `8` | sub-agent depth |
| `maxConcurrentSubagents` | unset | sub-agent concurrency |
| `webSearchesPerSession` | `9007199254740991` | web-search cap |
| `autoCompactWindow` | `100000` | auto-compact window |
| `tmuxTruecolor` | `true` | truecolor under tmux |
| `cleanupPeriodDays` | `36500` | transcript retention (also the managed value) |
| `requireManagedCleanup` | `true` | `pfm doctor` warns when the managed `cleanupPeriodDays` file is absent; `false` silences it |
| `compactNudge` | see `docs/design/hooks/hooks.md` | the compact-nudge hook |

## The rendered launch

```text
env -u {hygiene…} [CLAUDE_CONFIG_DIR={config dir}] {binary} {door verbs} \
  --settings '{"outputStyle":"default","cleanupPeriodDays":36500,"env":{…},"hooks":{…},"statusLine":{…},"subagentStatusLine":{…}[,"theme":…]}' \
  [--mcp-config '{"mcpServers":{"professor":{"type":"stdio","command":"{home}/.local/bin/pfm","args":["mcp","serve","--stdio"]}}}'] \
  [--model M] [--effort E] [--system-prompt-file F] [--allow-dangerously-skip-permissions --dangerously-skip-permissions]
```

## The system prompt

- **The composed file is built, not staged.** `make -C pfm prompts` (run by `dev.sh build pfm` and by `/pfm:release`) composes `share/head.md` + `{engine}/professor.md` + `share/tail.md` into the tracked `pfm/harness-prompts/composed/{claude,codex,opencode}.md`. `dev.sh test pfm` fails when a composed file differs from its parts.
- **Launch points at the repo.** `--system-prompt-file` is `{clone}/pfm/harness-prompts/composed/claude.md`, `{clone}` from the source-repo record. Nothing is copied at install.
- **Role seats** compose the claude prompt with the role into a seat file under the SID dir and pass that instead (`agentrole.ComposeSeatPrompt`); `pfm chat reload` refreshes it before respawning.
- **Missing file fails open:** the launch omits the flag and starts on Claude's own prompt; spawn-audit reports it.

## Doors

Each door builds a `claudelaunch.Request` — account, purpose, door verbs, per-launch choices — and calls `Render`.

### PlanClaude

`pfm chat new` and MCP `chat_new`. Verbs: `--session-id`, `--name`, the prompt positional. Per-launch: `--account`, `--cache 1h|5m`, `--model`, `--effort`, role seat file.

### HeadlessFork

`pfm chat branch`. Verbs: `--resume {id} --fork-session --name {name}`; model from the running chat. It records the launch once the fork's new session id resolves.

### Synthesize

The picker and `pfm chat open` / MCP `chat_open`: `NewClaude` (`--session-id`, prompt), `ResumeClaude` (`--resume {id}`), `Agent` (`pfm internal agent-open`). Per-launch: the picker's account and cache toggle, whose initial state is the config value.

### AgentOpen

`pfm internal agent-open` inside the pane: `--resume {id}`, direct exec.

### claudeRun

`pfm chat reload` respawns the pane in place: `--resume {id}`, `--model`/`--effort` when given, the refreshed seat file. Account and cache come from `--account`/`--cache` when given, else from the chat's [launch record](#the-launch-record); the respawn updates the record.

### LauncherRun

A `claude` typed at a shell, through [the managed launcher](#the-managed-launcher). Values from config; never the autonomy flags. A fresh launch gets an assigned `--session-id` and a launch record; `--resume {id}` records that id; a launch whose session id pfm cannot know (`--continue`) records nothing.

### PurposeQuery

`claude agents --json` for the reaper, inject-resume and agent-open listings: only the `--settings` payload and the hygiene list.

### Every door that starts a Claude session

1. Render doors: `PlanClaude`, `HeadlessFork`, `Synthesize` (fresh, resume and agent fallback), `AgentOpen` resume, `claudeRun` and `LauncherRun` call `claudelaunch.Render`.
2. Headless: `claudelaunch.RenderHeadless` supplies `--settings` for `pfm headless exec`, `pfm ask`, the limits ACK and the doctor capture.
3. Passthrough that starts a session: `pfm internal launch` supplies the two plugin values in its process environment.
4. VS Code terminal: its `PATH` finds pfm's `claude` shim, so a typed session takes a render or passthrough door.

Query-purpose `agents --json` and the exempt subcommands start no session.

### Doors pfm does not own

The VS Code Claude extension's panel is not a pfm launch door. pfm never launches it and writes none of its settings, so it carries no pfm launch value.

## The managed launcher

1. `~/.local/bin/claude` links to `~/.local/share/pfm/install/bin/claude`, which runs `pfm internal claude-launch "$@"`.
2. `claude-launch` resolves the real binary — `claude.binary`, else the newest native version under `~/.local/share/claude/versions/`, else `PATH` — never the launcher itself.
3. **Passthrough** — `PFM_LAUNCH_PASSTHROUGH=1`, already inside a pfm tmux socket, `-p`/`--print`/`--output-format`/`-h`/`--help`/`--version`/`-v`, or the subcommands `agents mcp update install doctor setup-token plugin config` — execs the real binary. Such a run carries no pfm settings except, when it starts a session, the two plugin env values in its process environment; the managed `cleanupPeriodDays` still applies to it.
4. Otherwise it renders `LauncherRun` into a fresh `cc-` tmux socket.

The `SessionStart` hook `launcher-repair` re-links a displaced launcher. Version retention keeps the newest two versions plus any a live process runs.

## The zsh shim

`~/.zshrc` sources `{clone}/pfm/internal/installer/assets/shim/pfm.zsh` directly; the file is static — nothing is rendered into it at install. It exports `CLAUDE_DISABLE_ADOPT=1`, `CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF=1`, `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=2` into the interactive shell, and defines `claude()`, which calls `~/.local/bin/claude` and closes the terminal after a TUI session, and `cx()`, which hands off to `pfm internal codex-launch`. The Codex launch reads each account's yolo setting from `pfm.config.json` and strips the registry's hygiene rows in Go; the shim carries no env list of its own.

## Long launch lines

A pane run over 8 KiB goes through a one-shot `/bin/sh` script under the tmux dir that deletes itself and `exec`s the run (`pfm/internal/tmux/launch.go`). The `--settings` payload with hooks is the common reason a run crosses the budget.

## pfm config claude

`pfm config claude [--account N]` prints every knob for that account — knob, wire, target, resolved value, and the source that won (`config`, `account`, `default`, `constant`). It is the answer to "what does a chat start with, and where do I change it".

## pfm doctor spawn-audit

Reads each live `cc-` chat's Claude argv from `/proc`, decodes it with `claudelaunch.Parse` — the inverse of `Render`, and the only parser of a Claude launch line — and classifies it:

| Verdict | Meaning |
| --- | --- |
| `INJECTED` | argv carries `--settings` with `outputStyle:"default"` and the registry's hook set, plus the prompt material the account's `systemPrompt` names (`--system-prompt-file`, or `env.CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT` inside the `--settings` payload) |
| `VIOLATION` | a fresh launch without the registry payload — a spawn site bypassed `Render` |
| `PREDATES-LAYER` | the process started before the current registry; reload to carry it |
| `ROLE-OK` / `ROLE-MISMATCH` / `ROLE-CHECK-FAILED` | a seat prompt file against its role |

Values set through the `--settings` `env` block are read from argv, never from `/proc/{pid}/environ`: Claude writes them into its environment after exec. A probe that cannot run reports `CHECK FAILED to run … — live chats unaudited`, never "no chats".

## Surfaces that stay in sync

- `claudelaunch.Knobs` ↔ `example.pfm.config.json` (every config-sourced knob has its key there) ↔ `pfm config claude` ↔ the mock engine's accepted flags (`pfm/internal/mockengine/claude.go`).
- The hygiene rows ↔ the Codex launch's unset list (`pfm internal codex-launch`) ↔ the headless strip lists ↔ the doctor harness probe's capture environment — all read from the registry.
- `outputStyle` constant ↔ the gate in `infra/check-self-hosted-manifest.sh`.
- `pfm/harness-prompts/composed/*.md` ↔ their parts.
