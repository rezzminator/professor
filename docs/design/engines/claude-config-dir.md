# Claude config dir

What pfm places on disk for Claude Code: one shared session store every account reads, the per-account dirs that keep only identity, the one managed setting in Claude Code's system directory, and the registry links. Launch-time values are not files — they ride the command line ([claude-launch.md](claude-launch.md)). Moving an older host onto this layout is [host-migration.md](host-migration.md).

`{config dir}` is one account's `configDir` from `pfm.config.json`; the implicit account's is `~/.claude`.

## Contents

- [Decisions](#decisions)
- [The session store](#the-session-store)
- [Account dirs](#account-dirs)
- [Managed settings](#managed-settings)
- [What pfm does not write](#what-pfm-does-not-write)
- [Registries](#registries)
- [Runtime writes](#runtime-writes)
- [Reads pfm depends on](#reads-pfm-depends-on)
- [pfm doctor checks](#pfm-doctor-checks)
- [Provisioned seats](#provisioned-seats)

## Decisions

- **Accounts are separate; sessions are one.** Each account keeps its own login and Claude state, so claude.ai connectors and Remote Control work per account. Everything keyed by a session id lives once, in `~/.claude`, and every other account dir links to it — a chat reloaded onto another account resumes with its transcript, rewind history and task list intact.
- **pfm owns the links.** `pfm install` creates them and `pfm doctor` fails when one is missing or points elsewhere. Code that reads sessions reads one root, `~/.claude/projects`.
- **Launch settings are flags, not files.** Hooks, status lines, MCP servers, `outputStyle` and the env block ride each launch. `pfm install` writes plugin state in account `settings.json`; the one-time move off an older layout also rewrites memory-helper paths and strips pfm-owned entries ([host-migration.md](host-migration.md#the-layout-table)). It writes no account `settings.local.json` or `.claude.json`.
- **One value is machine-wide.** `cleanupPeriodDays` is also a managed setting, because a Claude process started any way at all — passthrough, the VS Code extension, a direct binary path — must never run the 30-day transcript sweep.
- **Whatever install writes, doctor checks.**

## The session store

`SessionPaths` (`pfm/internal/installer/session_store.go`) names the entries keyed by session id:

| Entry | Holds |
| --- | --- |
| `projects/` | transcripts, `projects/{cwd slug}/{session id}.jsonl` |
| `file-history/` | rewind checkpoints, `file-history/{session id}/` |
| `tasks/` | task lists, `tasks/{session id}/` |
| `session-env/` | per-session environment captures, `session-env/{session id}/` |

- The store is `~/.claude`. For every account whose `configDir` is not `~/.claude`, each entry is a symlink: `{config dir}/projects → ~/.claude/projects`, and so on.
- An account whose whole `configDir` is a symlink to `~/.claude` (the implicit account's usual `~/.cc/1`) already satisfies every row.
- A transcript's path no longer names its account; the [launch record](claude-launch.md#the-launch-record) does.
- Transcript roots collapse to `~/.claude/projects` (`engine.claudeDefaultRoots`); no reader resolves symlinks to de-duplicate, and `PFM_CLAUDE_ROOTS` is a test-jail override only.

## Account dirs

Per account, owned by Claude Code: `.credentials.json`, `.claude.json` (login, onboarding, trust, the account's own MCP servers), `settings.json`, `sessions/` (live-process pid files), `history.jsonl`, `shell-snapshots/`, `paste-cache/`, `stats-cache.json`, `daemon*`, `telemetry/`, `plugins/`, `cache/`, `backups/`. `pfm install` runs Claude's plugin commands, which write `settings.json` and `plugins/`; the one-time host migration can also rewrite account settings.

## Managed settings

`pfm install` writes `pfm.json` into Claude Code's `managed-settings.d/` — `/etc/claude-code/managed-settings.d/` on Linux, `/Library/Application Support/ClaudeCode/managed-settings.d/` on macOS, the only managed location Claude reads there (`PFM_MANAGED_SETTINGS_DIR` overrides it in a test jail):

```json
{ "cleanupPeriodDays": 36500 }
```

- It is a drop-in: pfm owns only its own file in `managed-settings.d/` and never touches `managed-settings.json` or another file there.
- Writing it needs root; `pfm install` runs that one step through `sudo`, printing the exact command first, and continues without it when refused — doctor then warns. Linux runs `install -D`; BSD `install` has no `-D`, so macOS runs `mkdir -p` then `install`. The macOS path holds a space: every printed command quotes it.
- The value comes from `claude.cleanupPeriodDays`; the launch `--settings` carries the same value.
- Claude reads managed settings for every account and every launch path, and nothing below them overrides the value.

## What pfm does not write

- No launch setting or env key in any account `settings.json`. Hooks, `statusLine`, `subagentStatusLine` and `cleanupPeriodDays` ride `--settings` at launch.
- No entry in any `.claude.json`. The one `professor` stdio server (`pfm mcp serve --stdio`, serving every enabled family) rides `--mcp-config` at launch, beside the account's own servers.
- No `output-styles/`, `keybindings.json` or `CLAUDE.md`.
- The exception is Claude plugin state: `pfm install` runs `claude plugin marketplace add` and `claude plugin install` per account for `cache-live-control`, `sub-agent-compact` and `agent-effort`. Claude writes `{config dir}/plugins/**` and `enabledPlugins` in the account's physical `settings.json`. Both paths are journaled and restored at once if a command fails. A live chat on any account sharing that settings file skips the plugin step until the chat closes.

## Registries

| Path under `{config dir}` | Symlink to |
| --- | --- |
| `commands/reload.md` | `~/.local/share/pfm/install/reload.command.md` |
| `commands/*` | `{clone}/templates/global/commands/` entries |
| `skills/handoff/SKILL.md` | `~/.local/share/pfm/install/handoff.skill.md` |
| `skills/deep-rr` | `{clone}/workflows/deep-rr` |
| `skills/*` | `{clone}/templates/global/skills/` dirs holding a `SKILL.md` |
| `skills/{name}` (also `~/.agents/skills/{name}`) | `~/.local/share/pfm/install/skills/{name}/`, the shallow clone of a repo `templates/global/skills/sources.json` registers |
| `agents/*.md` | `{clone}/templates/global/agents/`, or a rendered variant under `~/.local/state/pfm/generated/claude-agents` |
| `themes/*` (`~/.claude` only) | regular files from `templates/themes/sources.json`, tracked in `theme-ownership.json` |

A link in the way is replaced, a regular file is backed up first, a foreign target is refused as a conflict; an orphan whose pfm target is gone is removed; uninstall removes what points at pfm's paths.

## Runtime writes

- `pfm archive` prunes archived sessions out of `~/.claude/history.jsonl` after a backup to the archive's `_sidecar-backups`.
- Kill's finisher removes `~/.claude/.cc-new-children/{id}` and `~/.claude/.cc-pane-children/{id}`.

## Reads pfm depends on

`{config dir}/.credentials.json` (account discovery, the usage hook's `api/oauth/usage` call), `{config dir}/sessions/{pid}.json` (which live process runs which session), `{config dir}/plugins/installed_plugins.json` and `settings.json` `enabledPlugins` (the plugin check), `~/.claude/projects/` (picker, `chat find`, archive), `.claude.json` `oauthAccount.emailAddress` (the duplicate-login check).

## pfm doctor checks

| Thing | Broken state reports |
| --- | --- |
| session store, per account and entry | `session-store: {config dir}/{entry} missing`, `… is a real dir ({n} entries) — run pfm install`, `… points at {target}, want ~/.claude/{entry}` |
| managed cleanup | `managed-cleanup: … missing`, `… cleanupPeriodDays={v}, want {config value}` |
| legacy account writes | `legacy: {file} still carries pfm {key} — run pfm install`; `legacy: {file} UNREADABLE error={cause}` |
| every other `HostLayout` row | `layout: {row} {verdict} {path}` for each row not `ok` — the same `ClassifyLayout` verdicts install acts on |
| registries | `agents/`, `commands/`, `skills/`: `state=missing\|conflict\|unreadable hint="run pfm install"` |
| Claude plugins, per account | `doctor: claude_plugins claude[{n}] plugin {id} not enabled\|not installed in {dir} — run pfm install --yes` |
| launcher | `launcher: missing`, `launcher: DISPLACED by {target}`, `launcher: unreadable error=…` — each with `run pfm install` |

### managed-cleanup

The missing-file line names the platform's `managed-settings.d/pfm.json` and warns that any Claude launch outside pfm can delete transcripts older than 30 days. `claude.requireManagedCleanup: false` silences the check; doctor prints `managed-cleanup: check off by config`.

### legacy

The account key is one of `hooks`, `statusLine`, `subagentStatusLine`, `mcpServers.chat`, `mcpServers.harvester` or `mcpServers.professor`; it would run twice beside the launch payload. An MCP entry is pfm's when `mcp-ownership.json` records it or, under `chat`, `harvester` or `professor`, when it matches an exact pfm shape ([host-migration.md](host-migration.md)). The `home-mcp` row reports `legacy: {home}/.mcp.json still carries pfm mcpServers.{name} — run pfm install` and `legacy: {ManagedRoot}/mcp-ownership.json still carries pfm clients — run pfm install`; an unparseable `~/.mcp.json` reports `layout: home-mcp UNREADABLE {path} error={cause}`. Memory helpers report `legacy: {file} names cc-memory-{wire|consolidate}.sh — run pfm install`.

## Provisioned seats

The demo and fence lane containers build seats from scratch (`infra/fence/lanes/root.sh` → `infra/demo/setup.sh`): machine config from `creds.sh` (`configDir: ~/.cc/{id}`, `systemPrompt: professor`), written where the container's `PFM_CONFIG` points, credentials copied mode 0600, then `pfm install --yes`, which creates the session-store links. `setup.sh` itself adds demo-only keys (onboarding, trust, permission-prompt skips, `tui`, `effortLevel`, `attribution`) that pfm never writes. `IS_SANDBOX=1` is exported where a root container launches with the bypass flags.
