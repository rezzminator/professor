# plugins

The five Claude Code plugins professor's marketplace lists, each a git submodule of its own public repository. This file is professor's; everything below `plugins/{name}/` belongs to that plugin's repository.

## Layout

- `agent-effort/`: a tag at the start of an Agent prompt sets that sub-agent's reasoning effort.
- `buddy/`: an ASCII companion above the prompt that reacts to the work and answers `/buddy`.
- `cache-live-control/`: `/cache` sets the prompt-cache TTL for the main chat and its sub-agents.
- `callmeter/`: records every tool call, request, agent and turn into SQLite (a Go binary behind every hook).
- `sub-agent-compact/`: per-party compaction policy, nudges, and self-compaction at a milestone the model picks.

Each `plugins/{name}/` is a full checkout of `https://github.com/rezzminator/{name}`. Its installable plugin sits at `plugins/{name}/plugins/{name}/`, and its root `.claude-plugin/marketplace.json` makes the repository its own marketplace.

## Which rules apply

- Inside `plugins/{name}/`, that plugin's own `CLAUDE.md` governs: its layout, gates, version bumps, branches and release steps. Read it before editing; this file never overrides it.
- Git inside a plugin follows `plugins/{name}/.claude/agents/gitter.md` when it exists, otherwise the machine gitter. The submodule mechanics (setup, gitlink bumps, push order, pull, worktree removal) are in professor's `.claude/agents/gitter.md`, section Submodules.
- A plugin's gates run from its own root: `npm test`, `npm run typecheck`, `npm run validate:plugin` for the TypeScript plugins; callmeter's `CLAUDE.md` names its Go gates. Professor's fence gate does not test plugin code; its leak gate leaves the submodules to their own repositories.

## Working in a plugin

- `.gitmodules` sets `branch = develop` and `ignore = dirty`: professor tracks only each plugin's commit (its gitlink), and an edit inside a plugin never shows in professor's `git status`. Read a plugin's state with `git -C plugins/{name} status`.
- `submodule update` leaves a plugin on a detached HEAD. Switch to `develop` (or a branch off it) before committing.
- A finished plugin change lands in two commits: the plugin's own commit on its `develop`, then a separate professor commit that bumps the gitlink (`chore(plugins): bump {name} to {short-sha}`).
- Push the plugin before professor. A professor gitlink to a commit the plugin's remote does not hold breaks every other clone.
- A professor worktree starts with empty `plugins/{name}/` folders. Run `git submodule update --init` in it before the fence mounts it read-only.
- Each repository is public: no machine-absolute paths, personal data or private project names in a tracked file.

## Installed copies

- Users install every plugin from its repository's `main` through the marketplace, so only a release reaches them.
- On this machine an alpha `pfm install` copies cache-live-control, sub-agent-compact and agent-effort from `plugins/{name}/plugins/{name}/` when `claude.pluginCheckoutRoot` in `pfm.config.json` names this directory, and disables their GitHub copies.
- `pfm install` does not copy callmeter or buddy. callmeter's `-dev` marketplace copy is refreshed by the steps in its own `CLAUDE.md`; buddy's local marketplace copy (`buddy-local`) is disabled, so the installed buddy is the GitHub one.
- Open chats pick up a refreshed plugin on `/reload-plugins` or a restart.
