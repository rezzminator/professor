# Claude config dir

`~/.claude` is the only shared store. Each account has its own identity directory and a link to the store for every shared entry. pfm installs registry items once into the store and supplies launch values through the command line ([claude-launch.md](claude-launch.md)).

`{config dir}` means one account's `configDir` from `pfm.config.json`, `~/.cc/{id}` by default. It never means `~/.claude`. `{store}` means `~/.claude`; `{clone}` means the recorded framework clone.

## Contents

- [Decisions](#decisions)
- [How Claude finds its files](#how-claude-finds-its-files)
- [The store](#the-store)
- [Account dirs](#account-dirs)
- [Settings](#settings)
- [MCP servers](#mcp-servers)
- [Launches outside pfm](#launches-outside-pfm)
- [Install build](#install-build)
- [Managed settings](#managed-settings)
- [What pfm does not write](#what-pfm-does-not-write)
- [Registries](#registries)
- [Runtime writes](#runtime-writes)
- [Reads pfm depends on](#reads-pfm-depends-on)
- [pfm doctor checks](#pfm-doctor-checks)
- [Provisioned seats](#provisioned-seats)

## Decisions

- **One store, separate identities.** Every account shares transcripts, rewind history, tasks, plans, pasted content and prompt history. Reloading onto another account resumes the same session through that account's links.
- **The store holds no login.** Every account, including account 1, has its own directory. The directory may be a symlink when it resolves outside the store. An account resolving to the store, or into it, is a blocking host check, and a dangling account link is an inspection error. `claudelaunch.InspectConfigDir` is the one rule that launch, install, the host checks and doctor apply.
- **pfm owns every shared link.** Install creates missing links and repoints drifted ones without touching their old targets. Doctor checks the store and every account's links.
- **Unknown entries stay where they are.** A name on neither list reports `UNCLASSIFIED`; pfm does not share or delete it. `EntryClass` also recognises ignored entries: `ide`, `.cc-new-children`, `.cc-pane-children`, `settings.local.json`.
- **Settings are shared by link.** Claude layers the shared user settings under project files, launch settings and managed policy. Per-account launch overrides live in `pfm.config.json`.
- **MCP definitions ride the launch.** `mcp.thirdParty` supplies operator servers beside pfm's `professor` entry. Trust, onboarding and login remain Claude's per-account state.
- **Retention applies outside pfm too.** The managed `cleanupPeriodDays` protects launches through other paths.

## How Claude finds its files

The config directory is `$CLAUDE_CONFIG_DIR`, falling back to `~/.claude` when unset. User memory, registries, plugins, rules, themes and user settings are read under that directory. pfm's account launches always set `CLAUDE_CONFIG_DIR`; shared entries reach them through their links.

Claude's state file is `${CLAUDE_CONFIG_DIR:-$HOME}/.claude.json`. With the variable unset it is `~/.claude.json`, outside the store. `settings.local.json` is a project file, `{project}/.claude/settings.local.json`, rather than an account settings file. Claude also checks `~/.claude/ide` for IDE locks. Claude writes user `settings.json` through its symlink, preserving the link.

## The store

`ClaudeStore`, `StoreEntries`, `AccountEntries` and `IgnoredEntries` live in `pfm/internal/installer/claude_store.go`. `StoreEntries`, in source order:

| Entry | Type | Holds |
| --- | --- | --- |
| `agents` | directory | user agents and pfm registry items |
| `commands` | directory | user commands and pfm registry items |
| `skills` | directory | user skills and pfm registry items |
| `rules` | directory | user rules |
| `plugins` | directory | the shared plugin installation |
| `themes` | directory | custom palettes |
| `projects` | directory | transcripts: `{cwd slug}/{session id}.jsonl` |
| `file-history` | directory | rewind checkpoints by session id |
| `tasks` | directory | task lists by session id |
| `session-env` | directory | per-session environment captures |
| `plans` | directory | plan files |
| `paste-cache` | directory | pasted content by content hash |
| `shell-snapshots` | directory | shell environment snapshots |
| `uploads` | directory | attached files |
| `downloads` | directory | fetched files |
| `teams` | directory | agent team state |
| `settings.json` | file | shared user settings; seed `{}\n` |
| `CLAUDE.md` | file | user memory; empty seed |
| `history.jsonl` | file | prompt history; empty seed |
| `stats-cache.json` | file | statistics cache; empty seed |
| `.last-cleanup` | file | cleanup marker; empty seed |
| `gh-pr-status-cache.json` | file | pull-request status cache; empty seed |

Every missing entry is created empty (apart from the `settings.json` seed). Existing entries are left intact. Transcript readers use `~/.claude/projects` (`engine.claudeDefaultRoots`); `PFM_CLAUDE_ROOTS` is a jail override. The transcript path does not identify its account; the [launch record](claude-launch.md#the-launch-record) does.

## Account dirs

`AccountEntries`, in source order, are never linked to the store:

| Entry | Holds |
| --- | --- |
| `.credentials.json` | login token |
| `.claude.json` | account identity, folder trust, onboarding and flags |
| `.claude.json.backup` | Claude state backup |
| `backups` | Claude state backups |
| `sessions` | live-process registry and process tokens |
| `daemon` | account daemon state |
| `daemon.log` | daemon log |
| `daemon-auth-status.json` | daemon authentication state |
| `daemon-auth-cooldown` | daemon authentication cooldown |
| `jobs` | daemon background jobs |
| `cache` | account model catalog |
| `state` | account state, including `mcp-discover-verdicts.json` |
| `mcp-needs-auth-cache.json` | MCP servers awaiting account OAuth |
| `telemetry` | queued account telemetry |
| `feedback` | queued account feedback |
| `.last-update-result.json` | the native updater's last result |

`.last-update-result.json` was a store entry until Claude's native updater was seen writing it by temp file and rename at the account path, which replaces a link with a real file. It is the first of `RetiredStoreEntries` (`pfm/internal/installer/retired_store_entries.go`): an entry once linked from every account and now per-account. A real file in an account dir is the healthy shape; a leftover link into the store and the store copy are `retired-store-entry` warnings, which `pfm install` migrates ([Install build](#install-build)). A future retirement is one more row in that list and its name in `AccountEntries`.

`sessions/{pid}.json` describes a live process, rather than the conversation transcript. On reload, the new process registers in the selected account's directory. For `state` inside the store, `store-identity` checks only `state/mcp-discover-verdicts.json`. While account 1's dir resolves to the store, each identity path in the store is the same file as its account path, so `store-identity` prints no delete: the entry moves into the real dir that `account-is-store`'s fix makes ([host-checks.md](host-checks.md#store-identity)). Unknown names in either the store or an account report `UNCLASSIFIED` and remain untouched.

## Settings

Claude's effective layers, highest first:

1. Managed settings, including pfm's retention drop-in.
2. Command-line flags and pfm's `--settings` payload.
3. The project's `.claude/settings.local.json`.
4. The project's `.claude/settings.json`.
5. The account's `settings.json` link to `~/.claude/settings.json`.

A settings `env` value overrides the shell's value; a flag overrides its settings key. Hooks merge across layers. `/config` writes shared user settings through the account link, so every account sees the change. An account-specific launch value belongs in that account's `claude` block in `pfm.config.json`. With `$HOME` as the working directory, the store's `settings.json` is also read as that project's `.claude/settings.json`.

## MCP servers

`--mcp-config` carries every `mcp.thirdParty` entry, plus pfm's `professor` server when `chat` or `harvester` is enabled. Third-party entries remain present when both families are off. The key is a map from server name to a JSON object in Claude's `mcpServers` shape; validation reserves `professor` for pfm.

The intended account `.claude.json` contains no `mcpServers`. Install does not import or strip definitions. A pfm-owned definition outside the launch is a blocking `pfm-mcp` check; another user-level entry is a `third-party-mcp` warning with the edit that puts it in `mcp.thirdParty` and removes it from the state file. The warning covers each account's state file and `~/.claude.json`.

## Launches outside pfm

- The terminal's `claude()` shell function calls pfm's managed launcher (`pfm/internal/installer/assets/shim/pfm.zsh`). Its rendered account launch chooses the ambient account directory or the primary account and sets `CLAUDE_CONFIG_DIR`. Passthrough preserves the ambient environment and supplies only the session plugin values; it does not choose an account.
- `pfm install --yes --vscode` sets `claudeCode.environmentVariables` in owned VS Code settings to include `CLAUDE_CONFIG_DIR` for the primary account. `vscode-ownership.json` records the prior value. Other environment entries are preserved; an operator edit to pfm's value relinquishes ownership. Uninstall restores the prior value only while pfm's value is still present.
- `pfm install` writes the login default, so a Claude started from a login shell, a systemd user service or a desktop launcher lands in account 1's dir (the roster's account 1, never a literal) instead of the store. `~/.config/environment.d/pfm-claude-config-dir.conf` carries `CLAUDE_CONFIG_DIR=${CLAUDE_CONFIG_DIR:-{config dir}}` and `PFM_CLAUDE_CONFIG_DIR_DEFAULT={config dir}`; `~/.profile` and `~/.zshenv` each carry one block fenced by `# BEGIN pfm claude-config-dir — installer-owned` and `# END pfm claude-config-dir — installer-owned` that sets and exports both only when `CLAUDE_CONFIG_DIR` is unset or empty (`if [ -z "${CLAUDE_CONFIG_DIR:-}" ]`). The condition is load-bearing: `~/.zshenv` runs in every zsh, including the shells of panes pfm launched on another account and every tool shell of a running chat. environment.d has no conditional that sets one variable on another's absence, so its sentinel is unconditional; `claudelaunch.InheritedConfigDir` counts the sentinel only where `CLAUDE_CONFIG_DIR` equals it, so a preset dir never reads as the default. Every pfm launch strips the sentinel and sets `CLAUDE_CONFIG_DIR` itself.
- Install creates a missing file, keeps every other line, replaces the block in place (appending it the first time; a changed existing file is backed up as `{file}.pre-professor-{stamp}`) and writes a symlinked startup file through to its target; a second install prints `ok` and changes nothing; a preview prints the same `change` lines and writes nothing. A host with no roster, or none with account 1, gets `skip    login default: {reason}`. Account 1's dir failing the launch refusal, a value with a space, quote, backslash, `$` or backtick, or a damaged fence (a begin with no end, an end with no begin, two begins) prints `FAIL    login default: …` and fails the run after every other step, the file untouched. Uninstall removes both blocks, deleting a file left empty, and the environment.d file.
- A bare binary launched without `CLAUDE_CONFIG_DIR` reads shared files from the store and state from `~/.claude.json`. Doctor warns about that home state file and blocks on identity entries inside the store. A bare binary needs an explicit account directory to use that account's identity.

## Install build

The [host checks](host-checks.md) run before any install write in preview, apply and `--check`. A blocking finding refuses the install; its fix is an operator action printed by doctor. Warnings allow install to continue.

After the gate, `wireClaudeStore` builds the shared entries and links:

1. Create missing store directories with mode `0700`; create missing files with mode `0600` and their `Seed`. Leave existing entries intact.
2. Create each missing account directory as a real directory with mode `0700`.
3. Create an absent shared-entry link to the store; replace a link pointing elsewhere; report a correct link as `ok`. Leave every real file or directory untouched: the host gate already refused that shape.

The install transcript uses `change  create {path}`, `change  link {config dir}/{entry} -> {store}/{entry}`, `change  repoint {config dir}/{entry} -> {store}/{entry} (was {old})`, and `ok      {config dir}/{entry}`.

Then `retireStoreEntries` migrates each `RetiredStoreEntries` entry:

1. In each account, by ID, remove a link at `{config dir}/{entry}` resolving to `{store}/{entry}`: `change  unlink {config dir}/{entry} (a per-account file now)`. A real file, a link elsewhere and an absent entry are left alone; Claude writes the account's own file.
2. After every account's link is gone, move `{store}/{entry}` into `{home}/.local/state/pfm/retired-store-entries/{entry}.pre-professor-{stamp}` (a `.N` suffix when taken): `change  archive {store}/{entry} -> {archive}`. Nothing is deleted. While an account resolves to the store, the copy stays: `skip    keep {store}/{entry}: account {id} {dir} resolves to the store — pfm doctor names the fix`.

A preview prints the same lines and writes nothing. A failed remove, mkdir or rename returns the error naming its paths. A second run finds nothing to do and prints nothing.

## Managed settings

`pfm install` writes `pfm.json` in `/etc/claude-code/managed-settings.d/` on Linux or `/Library/Application Support/ClaudeCode/managed-settings.d/` on macOS (`PFM_MANAGED_SETTINGS_DIR` overrides the directory in a jail):

```json
{ "cleanupPeriodDays": 36500 }
```

The value comes from `claude.cleanupPeriodDays` and also rides launch `--settings`. pfm owns only its drop-in. It attempts a direct write, then `sudo -n` when needed, printing the command; a refusal is advisory and doctor warns. Linux uses `install -D`; macOS uses `mkdir -p` and `install`, quoting the path's space. Managed policy applies through every launch path and outranks the other settings layers.

## What pfm does not write

pfm writes no launch setting or env key into user settings, no account `.claude.json` content, no `settings.local.json`, `output-styles/` or `keybindings.json`, and no user `CLAUDE.md` content. It creates the shared `CLAUDE.md` empty when absent. Account trust, onboarding and identity are Claude's.

The plugin step runs once through the primary account for `cache-live-control`, `sub-agent-compact` and `agent-effort`. Claude's plugin commands write shared `plugins/**` and `enabledPlugins` in shared `settings.json` through that account's links. An already installed and enabled plugin needs no command. A live chat on any account defers needed plugin commands; an unreadable live-process probe or a failed command returns an error.

On a host with an account roster the step's dir passes `claudelaunch.CheckConfigDir`, the refusal every launch applies: with no primary dir resolved, the fallback to install's config dir (the store) prints `FAIL    claude plugins in {store}: {store} resolves to the Claude store — run pfm doctor`, runs no command and fails the run after every later step. A preview passes a primary dir install would create first. A host with no roster, or `--config-dir`, keeps running the step in that dir, there an ordinary config dir. Each plugin command's environment sets `CLAUDE_CONFIG_DIR` and drops `PFM_CLAUDE_CONFIG_DIR_DEFAULT`.

## Registries

Each item is written once under the store. Every account sees the same agents, commands and skills through its shared-entry links.

| Path under `{store}` | Target or content |
| --- | --- |
| `commands/reload.md` | `~/.local/share/pfm/install/reload.command.md` |
| `commands/*` | `{clone}/templates/global/commands/` entries |
| `skills/handoff/SKILL.md` | `~/.local/share/pfm/install/handoff.skill.md` |
| `skills/*` | `{clone}/templates/global/skills/` dirs with a `SKILL.md` |
| `skills/{name}` | `~/.local/share/pfm/install/skills/{name}/` source clone |
| `agents/*.md` | clone sources or generated variants under `~/.local/state/pfm/generated/claude-agents` |
| `themes/*` | manifest-selected files; `theme-ownership.json` records ownership |

Source-fetched skills also link under `~/.agents/skills/{name}`. `templates/global/skills/sources.json` registers their repos; `templates/global/agents/` supplies agents; `templates/themes/sources.json` supplies themes.

A regular file in the way is backed up before replacement; an existing link can be replaced. Global registry inspection reports foreign targets as conflicts. `InspectDeadRegistryLinks` checks store `agents`, `commands`, `skills` and `~/.agents/skills` without following directory links: a dangling link is retired only when its resolved target belongs to a recorded clone, the managed install root or generated Claude agents. Foreign links remain. Unreadable paths return errors. Install and uninstall use this rule; doctor reports its findings. A retired path already absent prints nothing.

## Runtime writes

`pfm archive` prunes archived sessions from `~/.claude/history.jsonl` after a backup to the archive's `_sidecar-backups`. Kill's finisher removes `~/.claude/.cc-new-children/{id}` and `~/.claude/.cc-pane-children/{id}`.

## Reads pfm depends on

Account discovery and the usage hook read `{config dir}/.credentials.json`. Live-session lookup reads `{config dir}/sessions/{pid}.json`. Duplicate-login checks read `.claude.json` `oauthAccount.emailAddress`. Plugin checks read the store's `plugins/installed_plugins.json` and `settings.json` `enabledPlugins` once. Picker, find and archive read the shared `projects` root.

## pfm doctor checks

The authoritative detector list, severities, problems and operator fixes are in [host-checks.md](host-checks.md). Doctor prints `host-check: {severity} {check} {path} — {problem}` and a separate `host-check:   fix: {fix}` line. A `BLOCK` counts a failure; a `WARN` counts a warning. It separately checks the targets built by install:

| State | Output | Tally |
| --- | --- | --- |
| store entry missing | `store: {store}/{entry} missing — run pfm install` | failure |
| store entry unreadable | `store: {store}/{entry} UNREADABLE error={cause}` | failure |
| account directory missing | `account: {id} {config dir} missing — run pfm install` | failure |
| account directory unreadable | `account: {id} {config dir} UNREADABLE error={cause}` | failure |
| shared link missing | `account-link: {config dir}/{entry} missing — run pfm install` | failure |
| shared link elsewhere | `account-link: {config dir}/{entry} points at {target}, want {store}/{entry} — run pfm install` | failure |
| shared link unreadable | `account-link: {config dir}/{entry} UNREADABLE error={cause}` | failure |
| real shared entry | host-check `account-entry-real`; no duplicate target row | failure via host check |
| all targets clean | `account-links: ok ({a} accounts × {e} entries)` | none |

Managed retention reports `managed-cleanup: {path} missing` with the transcript-deletion warning, `cleanupPeriodDays={v}, want {config value}`, or `UNREADABLE error={cause}`. `claude.requireManagedCleanup: false` prints `managed-cleanup: check off by config`.

Registry checks cover `agents`, `commands` and `skills` with `state=missing|conflict|unreadable` and `hint="run pfm install"`, plus dead pfm links. Plugin checks run once on the store: `doctor: claude_plugins ok`, or `doctor: claude_plugins plugin {id} not enabled in {store}/settings.json — run pfm install --yes` / `doctor: claude_plugins plugin {id} not installed in {store} — run pfm install --yes`. Missing store settings print a skipped row; read errors count failures.

VS Code prints `doctor: vscode not managed (pfm install --vscode never ran)` when unmanaged. Owned settings with no account environment report `doctor: vscode settings={path} CLAUDE_CONFIG_DIR missing — run pfm install --yes --vscode`; a wrong value reports `CLAUDE_CONFIG_DIR={value}, want {primary dir}` with the same fix. When an operator's own `PFM` terminal profile would make `--vscode` refuse, both rows print `rename or remove the "PFM" terminal profile in {path}, then run pfm install --yes --vscode` instead; a profile matching a shape pfm itself once wrote is reclaimed by `--vscode`. Launcher checks report `doctor: launcher: missing`, `DISPLACED by {target}` or `unreadable error={cause}`, each with `run pfm install`.

## Provisioned seats

`infra/fence/lanes/provision.sh` writes a roster of separate fixture account directories at `PFM_CONFIG`, stages invented fixture credentials mode `0600` and per-account onboarding/trust state, then runs `pfm install --yes`. Install creates the store and every shared-entry link. The lane script then writes its presentation settings once to the store's `settings.json`.

`infra/demo/setup.sh` provisions the separate presentation demo. Demo settings include onboarding, trust, permission-prompt skips, `tui`, `effortLevel` and `attribution`. `IS_SANDBOX=1` is exported where a root container launches with bypass flags.
