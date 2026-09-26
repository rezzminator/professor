# Claude config dir

What pfm places on disk for Claude Code: one shared session store every account reads, the per-account dirs that keep only identity, the one managed setting at `/etc/claude-code/`, and the registry links. Launch-time values are not files — they ride the command line ([claude-launch.md](claude-launch.md)). Moving an older host onto this layout is [host-migration.md](host-migration.md).

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
- **Launch settings are flags, not files.** Hooks, status lines, MCP servers, `outputStyle` and the env block ride each launch, so `pfm install` writes no account `settings.json`, `settings.local.json` or `.claude.json`. The one exception is the one-time move off an older layout — memory-helper paths rewritten, pfm-owned entries stripped ([host-migration.md](host-migration.md#the-layout-table)); after it, pfm writes no key there.
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

Per account, owned by Claude Code and never written by pfm outside the one-time host migration: `.credentials.json`, `.claude.json` (login, onboarding, trust, the account's own MCP servers), `settings.json`, `sessions/` (live-process pid files), `history.jsonl`, `shell-snapshots/`, `paste-cache/`, `stats-cache.json`, `daemon*`, `telemetry/`, `plugins/`, `cache/`, `backups/`.

## Managed settings

`pfm install` writes `/etc/claude-code/managed-settings.d/pfm.json`:

```json
{ "cleanupPeriodDays": 36500 }
```

- It is a drop-in: pfm owns only its own file in `managed-settings.d/` and never touches `managed-settings.json` or another file there.
- Writing it needs root; `pfm install` runs that one step through `sudo`, printing the exact command first, and continues without it when refused — doctor then warns.
- The value comes from `claude.cleanupPeriodDays`; the launch `--settings` carries the same value.
- Claude reads managed settings for every account and every launch path, and nothing below them overrides the value.

## What pfm does not write

- No key in any account `settings.json`. Hooks, `statusLine`, `subagentStatusLine` and `cleanupPeriodDays` ride `--settings` at launch.
- No entry in any `.claude.json`. The one `professor` stdio server (`pfm mcp serve --stdio`, serving every enabled family) rides `--mcp-config` at launch, beside the account's own servers.
- No `output-styles/`, `plugins/`, `keybindings.json` or `CLAUDE.md`.

## Registries

| Path under `{config dir}` | Symlink to |
| --- | --- |
| `commands/reload.md` | `~/.local/share/pfm/install/reload.command.md` |
| `commands/*` | `{clone}/templates/global/commands/` entries |
| `skills/handoff/SKILL.md` | `~/.local/share/pfm/install/handoff.skill.md` |
| `skills/deep-rr` | `{clone}/workflows/deep-rr` |
| `skills/*` | `{clone}/templates/global/skills/` dirs holding a `SKILL.md` |
| `agents/*.md` | `{clone}/templates/global/agents/`, or a rendered variant under `~/.local/state/pfm/generated/claude-agents` |
| `themes/*` (`~/.claude` only) | regular files from `templates/themes/sources.json`, tracked in `theme-ownership.json` |

A link in the way is replaced, a regular file is backed up first, a foreign target is refused as a conflict; an orphan whose pfm target is gone is removed; uninstall removes what points at pfm's paths.

## Runtime writes

- `pfm archive` prunes archived sessions out of `~/.claude/history.jsonl` after a backup to the archive's `_sidecar-backups`.
- Kill's finisher removes `~/.claude/.cc-new-children/{id}` and `~/.claude/.cc-pane-children/{id}`.

## Reads pfm depends on

`{config dir}/.credentials.json` (account discovery, the usage hook's `api/oauth/usage` call), `{config dir}/sessions/{pid}.json` (which live process runs which session), `~/.claude/projects/` (picker, `chat find`, archive), `.claude.json` `oauthAccount.emailAddress` (the duplicate-login check).

## pfm doctor checks

| Thing | Broken state reports |
| --- | --- |
| session store, per account and entry | `session-store: {config dir}/{entry} missing`, `… is a real dir ({n} entries) — run pfm install`, `… points at {target}, want ~/.claude/{entry}` |
| managed cleanup | `managed-cleanup: /etc/claude-code/managed-settings.d/pfm.json missing — transcripts older than 30 days are deleted by any Claude launch outside pfm`, `… cleanupPeriodDays={v}, want {config value}`; silenced by `claude.requireManagedCleanup: false`, which doctor names as `managed-cleanup: check off by config` |
| legacy account writes | `legacy: {file} still carries pfm {hooks\|statusLine\|subagentStatusLine\|mcpServers.chat\|mcpServers.harvester} — run pfm install` (they would double-fire beside the launch payload); `legacy: {file} names cc-memory-{wire\|consolidate}.sh — run pfm install`; an account file that cannot be read or parsed reports `legacy: {file} UNREADABLE error={cause}`, never a clean row |
| every other `HostLayout` row | `layout: {row} {verdict} {path}` for each row not `ok` — the same `ClassifyLayout` verdicts install acts on |
| registries | `agents/`, `commands/`, `skills/`: `state=missing\|conflict\|unreadable hint="run pfm install"` |
| launcher | `launcher: missing`, `launcher: DISPLACED by {target}`, `launcher: unreadable error=…` — each with `run pfm install` |

## Provisioned seats

The demo and fence lane containers build seats from scratch (`infra/fence/lanes/root.sh` → `infra/demo/setup.sh`): machine config from `creds.sh` (`configDir: ~/.cc/{id}`, `systemPrompt: professor`), written where the container's `PFM_CONFIG` points, credentials copied mode 0600, then `pfm install --yes`, which creates the session-store links. `setup.sh` itself adds demo-only keys (onboarding, trust, permission-prompt skips, `tui`, `effortLevel`, `attribution`) that pfm never writes. `IS_SANDBOX=1` is exported where a root container launches with the bypass flags.
