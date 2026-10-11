# plugins — the Claude Code plugins professor's marketplace lists, each a git submodule

Professor tracks only each plugin's commit: an edit under `{name}/` belongs to that plugin's public repository, and a professor gitlink to a commit the plugin's remote lacks breaks every other clone.

# Vocabulary

- agent-effort: a tag at the start of an Agent prompt sets that sub-agent's reasoning effort · `agent-effort/`
- auto-effort: sets the main chat's reasoning effort each turn; the model moves it with `set_effort`, failing tests raise it, a cache gate per model · `auto-effort/`
- tool-visibility-controller: a `visibility` field in agent, skill and command frontmatter, plus a scope file, decides per loop which agents, skills, MCP tools and built-in tools it sees and may invoke · `tool-visibility-controller/`
- buddy: an ASCII companion above the prompt that reacts to the work and answers `/buddy` · `buddy/`
- cache-live-control: `/cache` sets the prompt-cache TTL of the main chat and its sub-agents · `cache-live-control/`
- callmeter: records every tool call, request, agent and turn into SQLite through a Go binary behind every hook · `callmeter/`
- sub-agent-compact: per-party compaction policy, nudges, and self-compaction at a milestone the model picks · `sub-agent-compact/`
- engine-sync: the Codex and OpenCode mirror writer pfm execs · `engine-sync/`
- installable plugin: the directory Claude Code installs, inside each checkout · `{name}/plugins/{name}/`
- gitlink: professor's pin of one plugin commit, registered with `branch = develop` and `ignore = dirty` · `../.gitmodules`

# Runtime

## Claude

- Under `{name}/`, Claude Code loads this file and that plugin's own `CLAUDE.md`; the plugin's file governs its layout, gates, branches and releases.

## Codex

- `AGENTS.md` here is generated from this file by engine-sync (`pfm codex build`).

## Host

- Users install each plugin from its repository's `main`.
- An alpha `pfm install` copies each plugin `claude.plugins` in `pfm.config.json` names bare from its installable plugin when `claude.pluginCheckoutRoot` names this directory, and disables its GitHub copy; a full `name@marketplace` id is installed as named.
- A plugin listed by full id is never copied: callmeter (`callmeter@callmeter-dev`) has its `-dev` copy refreshed by its own `CLAUDE.md`; buddy installs from its own dev marketplace as `buddy@buddy-dev`.
- `pfm install` copies no engine-sync: its wrapper downloads the checksum-verified release binary on first run and links the stable path pfm execs; `ENGINE_SYNC_BIN` points pfm at a local build, and the fence builds its own from `engine-sync/`.
- An open chat loads a refreshed copy on `/reload-plugins`.

# Rules

## Standalone

- Every plugin works fully on a machine without Professor: its code and every file it writes carry no Professor path, agent, policy or wording; credit in its README and manifest is the one exception.
- Professor-specific behavior enters through the plugin's own integration surface — a config file, flag, environment variable or extension hook — with a neutral default; Professor's install and project files supply Professor's values.

## Git

- Git inside a plugin goes through gitter, by `{name}/.claude/agents/gitter.md` where it exists, and by `../.claude/agents/gitter.md` § Submodules for setup, gitlink bumps, push order, pull and worktree removal.
- Read a plugin's state with `git -C plugins/{name} status`: `ignore = dirty` keeps plugin edits out of professor's `git status`.

## Testing

- Each plugin's gates run from its own root, by its own `CLAUDE.md`; professor's fence gate tests no plugin code.
