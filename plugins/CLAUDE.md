# plugins — the Claude Code plugins professor tracks, each a git submodule, all but gpt-gateway listed in its marketplace

Professor tracks only each plugin's commit: an edit under `{name}/` belongs to that plugin's public repository, and a professor gitlink to a commit the plugin's remote lacks breaks every other clone.

# Vocabulary

- auto-effort-control: reasoning effort for the main chat and the sub-agents it launches — the main chat's per turn, moved by the model's `set_effort`, raised by failing tests, a cache gate per model; a tag at the start of an Agent prompt sets that sub-agent's; an agent whose frontmatter carries `effortControl` is raised at each failing test run or red sub-task, brought home once the trouble resolves, and moves its own with `effort_up`, `effort_down`, `effort_auto` · `auto-effort-control/`
- tool-visibility-controller: a `visibility` field in agent, skill and command frontmatter, plus a scope file, decides per loop which agents, skills, MCP tools and built-in tools it sees and may invoke · `tool-visibility-controller/`
- buddy: an ASCII companion above the prompt that reacts to the work and answers `/buddy` · `buddy/`
- cache-live-control: `/cache` sets the prompt-cache TTL of the main chat and its sub-agents · `cache-live-control/`
- cache-keepwarm: keeps the prompt cache of the main chat and waiting sub-agents warm through background waits with visible pokes, capped per wait and each admitted by a budget against the rewrite it saves · `cache-keepwarm/`
- callmeter: records every tool call, request, agent and turn into SQLite through a Go binary behind every hook · `callmeter/`
- forward-compaction: compaction where the model writes its own frontier — the main chat and every sub-agent through one tool — installed with the person's standing orders and the newest messages after the call that fit, verbatim · `forward-compaction/`
- engine-sync: the Codex and OpenCode mirror writer pfm execs · `engine-sync/`
- harvester: shared engine MCP daemon and one-shot web/document CLI; its own plugin registers the MCP server `harvester` (`mcp__plugin_harvester_harvester__*` in Claude Code), pfm none · `harvester/`
- gpt-gateway: a local passthrough between Claude Code and the Anthropic API, with a middleware hook API and an opt-in ChatGPT route; a tracked submodule the marketplace does not list · `gpt-gateway/`
- installable plugin: the directory Claude Code installs, inside each checkout · `{name}/plugins/{name}/`
- gitlink: professor's pin of one plugin commit, registered with `branch = develop` and `ignore = dirty` · `../.gitmodules`

# Runtime

## Claude

- Under `{name}/`, Claude Code loads this file and that plugin's own `CLAUDE.md`; the plugin's file governs its layout, gates, branches and releases.

## Codex

- `AGENTS.md` here is generated from this file by engine-sync (`engine-sync codex build`).

## Host

- Users install each plugin from its repository's `main`.
- An alpha `pfm install` installs every plugin `.claude-plugin/marketplace.json` lists as `{name}@{name}-dev`, copied from `{name}/plugins/{name}` here (or under `claude.pluginCheckoutRoot` when set) into `~/.local/share/{name}-dev`, and disables its GitHub copies. It installs each checkout's working tree as it sits on disk, released or not — unpushed commits, uncommitted edits, no alpha tag — and no plugin's local state stops `/pfm:update`, which moves a gitlink only by a clean fast-forward of a clean checkout.
- On alpha, `claude.plugins` in `pfm.config.json` is the enabled set, every marketplace plugin when unset; a plugin it leaves out installs disabled; a full `name@marketplace` id there is installed as named, never copied.
- A plugin option is also an environment variable `{PLUGIN}_{OPTION}`, both names upper snake (`AUTO_EFFORT_CONTROL_FLOOR`): variable > the plugin's own option (`pluginConfigs`, `/config`, callmeter's `config.json`) > default; each plugin's README lists its settings.
- forward-compaction and buddy declare no `userConfig`: forward-compaction takes its options only from its `FORWARD_COMPACTION_*` variables, set from `claude.env`; buddy reads no variable and keeps every setting in `${XDG_CONFIG_HOME:-$HOME/.config}/buddy/config.json`.
- `pfm install` writes `claude.env` from `pfm.config.json` into the primary seat's `settings.json` `env`, which every seat shares, owning only the keys it wrote (ledger `~/.local/share/pfm/install/claude-env-ownership.json`); `pfm doctor` reports a key changed or dropped by hand.
- On alpha, pfm builds callmeter from its source checkout into its versioned binary home before refreshing its plugin copy.
- `pfm install` provisions harvester independently of Claude: alpha builds from source, stable verifies release checksums; `~/.local/bin/harvester` links its stable binary for every engine.
- `pfm install` copies no engine-sync binary, its plugin copy carrying only the wrapper: `make -C pfm host-install` builds one from `engine-sync/` on disk into `{engine-sync home}/versions/{version}/engine-sync` and points the stable link `bin/engine-sync`, which pfm execs, at it (`pfm/scripts/install-engine-sync.sh`); the wrapper the hooks run downloads its plugin version's checksum-verified release binary on first run, runs that, and links the stable path only when no newer version holds it; `ENGINE_SYNC_BIN` points pfm and the hooks at a local build; the fence builds its own from `engine-sync/`.
- Chats run each `{name}@{name}-dev` plugin from its folder, the `-dev` copy `~/.local/share/{name}-dev/plugins/{name}` (a `directory` marketplace), never from `{config}/plugins/cache/`: a copy refresh reaches a new chat or an open chat's `/reload-plugins` with no version bump, and a running chat's next hook call can already run the new files.
- A released `{name}@{name}` (stable) runs from Claude Code's versioned cache `{config}/plugins/cache/{name}/{name}/{version}`.

# Rules

## Standalone

- Dependency runs one way: pfm may depend on a plugin; a plugin depends on neither pfm nor Professor, and installs and works fully on a machine without them.
- A plugin's code and every file it writes carry no Professor path, agent, policy or wording; credit in its README and manifest is the one exception.
- Professor-specific behavior enters through the plugin's own integration surface — a config file, flag, environment variable or extension hook — with a neutral default; Professor's install and project files supply Professor's values; a new option adds its `{PLUGIN}_{OPTION}` variable, read in a function-hooks plugin by a string-literal `$.env.get('NAME')`.

## Identity

- Every plugin belongs to professor: listed in the marketplace and installed as `{name}@professor`.

## Native first

- A plugin uses the native APIs and utilities of Claude Code's plugin system and reimplements none of them: a context's token count is read from Claude Code's own report; the plugin counts and estimates none.

## Logging

- All plugin logging goes through one shared plugin-log library, shipped inside each plugin as an identical vendored copy of one canonical source, so the plugin still installs and runs without Professor or pfm; a library change lands in the canonical source and every copy in the same pass.
- Directory: every plugin MUST write its logs under `$PLUGIN_LOG_DIR/{plugin}` when `PLUGIN_LOG_DIR` is set, and no plugin-specific log variable or path overrides it; else `~/.local/state/{plugin}/logs`; with no directory resolvable, logging is off.
- Level files `errors.jsonl`, `warnings.jsonl`, `info.jsonl`, directory 0700, files 0600.
- Row: one JSON line, keys in order `ts`, `plugin`, `level`, `event`, `msg`, `where`, `session` when known, `detail`; at most 4096 bytes with its newline, shortened to fit.
- A level file past 20971520 bytes (20 MiB) moves to `.1`, and a `log-capped` row goes to `warnings.jsonl`.
- The screen shows only errors and one line per failing streak saying the log is off or failing, re-armed by the next success; warning, info and debug rows go to their files, or nowhere when the log cannot be written.

## Git

- Git inside a plugin goes through gitter, by `{name}/.claude/agents/gitter.md` where it exists, and by `../.claude/agents/gitter.md` § Submodules for setup, gitlink bumps, push order, pull and worktree removal.
- Read a plugin's state with `git -C plugins/{name} status`: `ignore = dirty` keeps plugin edits out of professor's `git status`.

## Testing

- Each plugin's gates run from its own root, by its own `CLAUDE.md`; professor's fence gate tests no plugin code.
