# plugins — the Claude Code plugins professor's marketplace lists, each a git submodule

Professor tracks only each plugin's commit: an edit under `{name}/` belongs to that plugin's public repository, and a professor gitlink to a commit the plugin's remote lacks breaks every other clone.

# Vocabulary

- auto-effort-control: reasoning effort for the main chat and the sub-agents it launches — the main chat's per turn, moved by the model's `set_effort`, raised by failing tests, a cache gate per model; a tag at the start of an Agent prompt sets that sub-agent's; an agent whose frontmatter carries `effortControl` is raised at each failing test run or red sub-task, brought home once the trouble resolves, and moves its own with `effort_up`, `effort_down`, `effort_auto` · `auto-effort-control/`
- tool-visibility-controller: a `visibility` field in agent, skill and command frontmatter, plus a scope file, decides per loop which agents, skills, MCP tools and built-in tools it sees and may invoke · `tool-visibility-controller/`
- buddy: an ASCII companion above the prompt that reacts to the work and answers `/buddy` · `buddy/`
- cache-live-control: `/cache` sets the prompt-cache TTL of the main chat and its sub-agents · `cache-live-control/`
- callmeter: records every tool call, request, agent and turn into SQLite through a Go binary behind every hook · `callmeter/`
- sub-agent-compact: per-party compaction policy, nudges, and self-compaction at a milestone the model picks · `sub-agent-compact/`
- engine-sync: the Codex and OpenCode mirror writer pfm execs · `engine-sync/`
- harvester: shared engine MCP daemon and one-shot web/document CLI pfm registers as `harvester-professor` · `harvester/`
- installable plugin: the directory Claude Code installs, inside each checkout · `{name}/plugins/{name}/`
- gitlink: professor's pin of one plugin commit, registered with `branch = develop` and `ignore = dirty` · `../.gitmodules`

# Runtime

## Claude

- Under `{name}/`, Claude Code loads this file and that plugin's own `CLAUDE.md`; the plugin's file governs its layout, gates, branches and releases.

## Codex

- `AGENTS.md` here is generated from this file by engine-sync (`engine-sync codex build`).

## Host

- Users install each plugin from its repository's `main`.
- An alpha `pfm install` installs every plugin `.claude-plugin/marketplace.json` lists as `{name}@{name}-dev`, copied from `{name}/plugins/{name}` here (or under `claude.pluginCheckoutRoot` when set) into `~/.local/share/{name}-dev`, and disables its GitHub copies.
- On alpha, `claude.plugins` in `pfm.config.json` is the enabled set, every marketplace plugin when unset; a plugin it leaves out installs disabled; a full `name@marketplace` id there is installed as named, never copied.
- A plugin option is also an environment variable `{PLUGIN}_{OPTION}`, both names upper snake (`AUTO_EFFORT_CONTROL_FLOOR`): variable > the plugin's own option (`pluginConfigs`, `/config`, callmeter's `config.json`) > default; each plugin's README lists its settings.
- sub-agent-compact and buddy declare no `userConfig`: sub-agent-compact reads only its `SUB_AGENT_COMPACT_*` variables, set from `claude.env`; buddy reads no variable and keeps every setting in `${XDG_CONFIG_HOME:-$HOME/.config}/buddy/config.json`.
- `pfm install` writes `claude.env` from `pfm.config.json` into the primary seat's `settings.json` `env`, which every seat shares, owning only the keys it wrote (ledger `~/.local/share/pfm/install/claude-env-ownership.json`); `pfm doctor` reports a key changed or dropped by hand.
- On alpha, pfm builds callmeter from its source checkout into its versioned binary home before refreshing its plugin copy.
- `pfm install` provisions harvester independently of Claude: alpha builds from source, stable verifies release checksums; `~/.local/bin/harvester` links its stable binary for every engine.
- `pfm install` copies no engine-sync: its wrapper downloads the checksum-verified release binary on first run and links the stable path pfm execs; `ENGINE_SYNC_BIN` points pfm at a local build, and the fence builds its own from `engine-sync/`.
- Chats run Claude Code's versioned cache `{config}/plugins/cache/{name}-dev/{name}/{version}`, never the `-dev` copy: a copy refresh alone reaches no chat, and a content change reaches the cache only with a version bump.
- `pfm install` moves each stale cache entry with `claude plugin update`: a new chat gets the new version at once, an open chat on `/reload-plugins`; a running chat keeps its old snapshot, marked `.orphaned_at`.

# Rules

## Standalone

- Every plugin works fully on a machine without Professor: its code and every file it writes carry no Professor path, agent, policy or wording; credit in its README and manifest is the one exception.
- Professor-specific behavior enters through the plugin's own integration surface — a config file, flag, environment variable or extension hook — with a neutral default; Professor's install and project files supply Professor's values; a new option adds its `{PLUGIN}_{OPTION}` variable, read in a function-hooks plugin by a string-literal `$.env.get('NAME')`.

## Git

- Git inside a plugin goes through gitter, by `{name}/.claude/agents/gitter.md` where it exists, and by `../.claude/agents/gitter.md` § Submodules for setup, gitlink bumps, push order, pull and worktree removal.
- Read a plugin's state with `git -C plugins/{name} status`: `ignore = dirty` keeps plugin edits out of professor's `git status`.

## Testing

- Each plugin's gates run from its own root, by its own `CLAUDE.md`; professor's fence gate tests no plugin code.
