# plugins — the five Claude Code plugins professor's marketplace lists, each a git submodule
Professor tracks only each plugin's commit: an edit under `{name}/` belongs to that plugin's public repository, and a professor gitlink to a commit the plugin's remote lacks breaks every other clone.

# Vocabulary
- agent-effort: a tag at the start of an Agent prompt sets that sub-agent's reasoning effort · `agent-effort/`
- buddy: an ASCII companion above the prompt that reacts to the work and answers `/buddy` · `buddy/`
- cache-live-control: `/cache` sets the prompt-cache TTL of the main chat and its sub-agents · `cache-live-control/`
- callmeter: records every tool call, request, agent and turn into SQLite through a Go binary behind every hook · `callmeter/`
- sub-agent-compact: per-party compaction policy, nudges, and self-compaction at a milestone the model picks · `sub-agent-compact/`
- installable plugin: the directory Claude Code installs, inside each checkout · `{name}/plugins/{name}/`
- gitlink: professor's pin of one plugin commit, registered with `branch = develop` and `ignore = dirty` · `../.gitmodules`

# Runtime
## Claude
- Under `{name}/`, Claude Code loads this file and that plugin's own `CLAUDE.md`; the plugin's file governs its layout, gates, branches and releases.
## Codex
- `AGENTS.md` here is generated from this file by `pfm codex build`.
## Host
- Users install each plugin from its repository's `main`.
- An alpha `pfm install` copies agent-effort, cache-live-control and sub-agent-compact from their installable plugin when `claude.pluginCheckoutRoot` in `pfm.config.json` names this directory, and disables their GitHub copies.
- `pfm install` copies neither callmeter nor buddy: callmeter's `-dev` marketplace copy is refreshed by its own `CLAUDE.md`; buddy's local copy `buddy-local` is disabled.
- An open chat loads a refreshed copy on `/reload-plugins`.

# Rules
## Git
- Git inside a plugin goes through gitter, by `{name}/.claude/agents/gitter.md` where it exists, and by `../.claude/agents/gitter.md` § Submodules for setup, gitlink bumps, push order, pull and worktree removal.
- Read a plugin's state with `git -C plugins/{name} status`: `ignore = dirty` keeps plugin edits out of professor's `git status`.
## Testing
- Each plugin's gates run from its own root, by its own `CLAUDE.md`; professor's fence gate tests no plugin code.
