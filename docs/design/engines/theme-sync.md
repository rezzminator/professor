# Theme sync

`pfm theme` renders one pfm-owned palette into the native format of every surface the user looks at, reloads each live, reads each back, and stamps all of it with one generation number, so "in sync" is a fact the status matrix proves. The engine is `pfm/internal/themesync`; the verb and the name-sync door are `pfm/internal/themecmd`; the tmux seam is `pfm/internal/themetmux`. It ships inert: nothing is read, written or reloaded until `pfm theme set` writes `state.json`.

## Contents

- [Verb](#verb)
- [Palettes](#palettes)
- [State directory](#state-directory)
- [Engine](#engine)
- [Verdicts](#verdicts)
- [Surfaces](#surfaces)
- [tmux doors](#tmux-doors)
- [Install hold](#install-hold)

## Verb

### RunVerb

`pfm theme [status|ls|set <palette> [--appearance auto|dark|light]|sync [--force]|off]`. Bare `pfm theme` is `status`. `--json` prints the `themesync.Report` on `status`, `set`, `sync` and `off`; `help` prints the usage. Options may follow the positional arguments (`cli.ParseFlagsAnywhere`).

| Verb | Does |
| --- | --- |
| `status` | `themesync.ReadBack`: reads every surface back; writes nothing |
| `ls` | every palette with its dark and light swatches (sample, ANSI 0-7, four state colours); the palette in force is marked `*` |
| `set <palette>` | `themesync.Set`: the first call turns the sync on; a later call changes palette or appearance |
| `sync` | `themesync.Sync`: re-resolves the appearance and re-applies every surface when it changed; otherwise re-applies only the surfaces whose read-back is DRIFT or ERROR, writing nothing (`state.json` included) when none is; `--force` re-applies every surface regardless |
| `off` | `themesync.Off`: restores every surface from its snapshot and disables the sync |

Exit codes: `0` ok; `1` when any surface reads DRIFT or ERROR (`Report.OK`) or a run failed (error on stderr as `pfm theme: …`); `2` usage: an unknown subcommand, a stray positional argument, a bad `--appearance`, or an unknown palette, whose message names every known palette (`pfm theme set: unknown palette "x" — known: tokyo-night, solarized, catppuccin`). PENDING, ABSENT, APPLIED, IN-SYNC and RESTORED never raise the exit code.

With the sync never set (no `state.json`, or `enabled` false), `status` and `sync` print `pfm theme: off — pfm theme set <palette> turns it on` (the `offLine` constant) and exit 0; `--json` prints a report with `enabled` false and an empty `surfaces` array. `off` always runs: with no `state.json` it touches nothing and prints the same line; with a disabled `state.json` it restores whatever an earlier half-finished run left.

### drawMatrix

Text output is a header `pfm theme · {palette} · {mode} → {appearance} · gen {N}` (or `pfm theme · off` after an off), one row per surface (`surface  VERDICT  g{N}|-  path  detail`; verdicts coloured green, yellow for PENDING, dim for ABSENT, red otherwise, only on a truecolor terminal without `NO_COLOR`), and a closing proof line from `proofLine`: `in sync · {n} of {total} surfaces at gen {N}` (with `· {k} pending` and `· {k} absent` appended) or `OUT OF SYNC · {surface} {VERDICT}, …`.

`themecmd.Main` builds the `themesync.Env`: `Home`, `StateDir` (`DefaultStateDir`), `PfmBinary` (`~/.local/bin/pfm`, the stable launcher), the `obs`-wrapped runner and `themetmux.AtDir(values.TmuxDir)`.

## Palettes

### Palettes

`themesync.Palettes()` lists the embedded palettes in display order; `PaletteByName` returns one or an error naming every known name.

| Name | Title | Dark | Light | Herdr built-ins (dark / light) |
| --- | --- | --- | --- | --- |
| `tokyo-night` | Tokyo Night | TokyoNight Night | TokyoNight Day | `tokyo-night` / `tokyo-night-day` |
| `solarized` | Solarized | iTerm2 Solarized Dark | iTerm2 Solarized Light | `solarized` / `solarized-light` |
| `catppuccin` | Catppuccin | Mocha | Latte | `catppuccin` / `catppuccin-latte` |

A `Variant` is one side: `Background`, `Foreground`, `Cursor`, `SelectionBackground`, `SelectionForeground`, the UI roles `Surface`, `Overlay`, `Muted`, `Accent`, `ANSI` (16 colours) and `State`. `withState` derives the four agent-state colours identically for every surface: `blocked` is ANSI 1 (red), `working` ANSI 3 (yellow), `done` ANSI 2 (green), `idle` the variant's `Muted`. `Set` validates both variants of the chosen palette (`validate`: every colour a lowercase `#rrggbb`) before touching anything.

### DetectAppearance

`--appearance auto` (the default, `ModeAuto`) follows macOS: `defaults read -g AppleInterfaceStyle` printing `Dark` is dark, exit 1 with "does not exist" is light. Any other result is an error, never a guessed side. `dark` and `light` (`ModeDark`, `ModeLight`) hold one side and skip detection.

## State directory

`~/.local/state/pfm/theme/` (`DefaultStateDir`):

| File | Holds |
| --- | --- |
| `state.json` | `enabled`, `palette`, `mode`, `appearance`, `generation`, one snapshot per edited user file (`claude`, `herdr`, `ghostty`, `vscode`), `due` (surfaces whose live reload failed), and `offPending` (an off left a surface unrestored); written atomically, mode 0600 |
| `current.json` | the VS Code contract the extension watches (`vscodeContract`) |
| `vscode-ack.json` | the extension's acknowledgement of a generation |
| `vscode-applied.json` | the extension's record of each colour key it set with the value it replaced |
| `pfm-theme.lock` | the flock serialising `set`, `sync` and `off` |
| `backups/` | pre-set copies of the Claude theme files (`backups/claude/{file}`) |
| `follow.log` | stdout and stderr of the appearance follower's launch agent |

Edited user files that live elsewhere (Herdr's `config.toml`, Ghostty's config, VS Code `settings.json`) are backed up beside themselves as `{file}.pfm-theme-{YYYYMMDD-HHMMSS}` the first time the sync edits them. A user file that is a symlink (a dotfiles checkout) is written at the file it resolves to, so the link stays a link through `set` and `off`. The extension also uses a `vscode-apply.lock` directory here while it merges settings.

### SyncOn

`SyncOn(stateDir)` is the one gate every door calls: false when `state.json` does not exist, the stored `enabled` otherwise. A `state.json` that exists but cannot be read or parsed reports true, so a door errs toward running and surfacing the error.

## Engine

### Set

Resolves the palette, mode and appearance, takes the lock, loads the state and bumps `generation` when the sync was off, new, or the palette, mode or appearance changed (a repeat `set` with identical arguments keeps the generation). It persists the state, then `applyAll`.

### Sync

Re-resolves the appearance from the stored mode. A changed appearance bumps the generation and runs `applyAll`. Otherwise it reads every surface back and, unless `--force` or a DRIFT/ERROR row says otherwise, writes nothing and reports the read-back.

### ReadBack

Reads every surface against the state's generation; no lock, no write.

### Off

Bumps the generation (when it was enabled), sets `enabled` false, runs every surface's restore, clears `due`, sets `offPending` exactly when a restore row is ERROR, and persists. `state.json` stays, disabled. `RestorePending(stateDir)`, the uninstall's gate, is `SyncOn` plus `offPending` or any file snapshot left behind, so an off that failed on a tmux server or the follower, which keep no snapshot, runs again on the rerun.

### surfaces

`session.surfaces()` fixes the matrix row order: `claude`, `tmux`, `herdr`, `ghostty`, `vscode`, `follower`. Each surface has three operations: apply (render and reload), read (read back), restore (undo from the snapshot). `applyAll` applies then reads each surface back; an apply that was deferred reads APPLIED with its note rather than a bare IN-SYNC. A surface whose live reload failed is recorded in `due`; it keeps reading ERROR (`live reload failed earlier; the next sync retries it.`) until a later reload succeeds.

## Verdicts

`themesync.Verdict`, per `SurfaceResult` (`surface`, `verdict`, `generation`, `path`, `detail`):

| Verdict | Meaning |
| --- | --- |
| `IN-SYNC` | the surface read back equals the render at the state's generation (`generation` is what the surface itself reports; 0 when it carries none) |
| `APPLIED` | written (and reloaded) this run, or the reload is deferred and `detail` says when it takes effect |
| `RESTORED` | `off` put the surface back as it was |
| `PENDING` | VS Code only: the extension has not acknowledged the generation yet |
| `ABSENT` | the surface is not installed or not applicable; `detail` names what was looked for |
| `DRIFT` | the surface exists but differs from the render, or the extension's ack is stale |
| `ERROR` | the surface could not be read or written; `detail` carries the cause |

The read-back is generation-stamped where the surface can carry a stamp: the tmux option `@pfm-theme-generation`, the Herdr block header `gen {N}`, the Ghostty include file header, the VS Code ack's `generation`. Claude files carry none; they compare byte-for-byte against the render and report the state's generation.

## Surfaces

### surfaceClaude

**Surface:** `claude` — **Files:** `~/.claude/themes/professor-*.json` — **Live reload:** none; the files are the whole apply

Render: each existing `professor-*.json` is re-rendered as `{name, base, overrides}` from its pre-set snapshot: name is the original name plus `· {palette title}`, `base` is `dark` or `light` for the appearance, and every token comes from `claudeRoles` (accent, ANSI hues and state colours, diff and message backgrounds blended toward the background, shimmer toward the foreground). The token set is the keys of the installed `~/.claude/themes/tokyo-night.json` when present, else the embedded 47-token list (`claudeTokenKeys`); a token the roles do not know keeps its base value. The overlay keys `promptBorder` and `promptBorderShimmer` keep the pre-set file's value when it had one.

Snapshot: the first apply copies each file to `backups/claude/{file}` and records it in `state.claude`.

Read-back: re-renders from the snapshot and compares bytes; a differing file is DRIFT, and so is a `professor-*.json` present now but not held (installed after `set`: `{file} is not themed`), which the next sync themes and snapshots. A held file the user deleted is let go at the next apply, so `off` does not bring it back. It also reads `~/.cc/*/settings.json` and `~/.claude/settings.json`: accounts whose `theme` is not `custom:professor-*` are listed in `detail` as `accounts not following`; that alone leaves IN-SYNC while at least one account selects a professor theme, and is DRIFT when none does or a file also differs. No `professor-*.json` present and none held: ABSENT.

Off: writes the snapshot bytes back; RESTORED with `{n} themes`.

### surfaceTmux

**Surface:** `tmux` — **Targets:** every live pfm chat server (sockets in the chat tmux directory) — **Live reload:** `set-option -g` takes effect at once

Render (`tmuxOptions`): 8 style options (`status-style`, `window-status-style`, `window-status-current-style`, `pane-border-style`, `pane-active-border-style`, `message-style`, `message-command-style`, `mode-style`) and 13 user options: `@pfm-theme`, `@pfm-theme-generation`, `@pfm-c-{bg,fg,surface,overlay,muted,accent}`, `@pfm-state-{blocked,working,done,idle}` and `@pfm-state-colour`. The current-window and active-border styles use `tmuxStateFormat`, a conditional format on `@pfm-state` that picks the state colour (`blocked`, `working`, `done`, else the idle colour).

Apply (`tmuxApplySocket`): probe liveness (`status-style`; a dead socket is an error naming it), capture each style's prior value as `@pfm-theme-prior-{option}` on a server not yet carrying `@pfm-theme-generation` (stored as `v:{value}` so an empty prior still reads as captured; a themed server's priors are never read again, so a failed read cannot overwrite one with a themed value), then set every option in one apply per server. A partial failure reports `{n} of {k} servers failed`.

Read-back: every option on every server against the render; a value mismatch is DRIFT, an unreadable option ERROR; the generation reported is `@pfm-theme-generation`. No live server: ABSENT (`no live pfm chat servers`).

Off: restores each style to its captured prior (unsets it when none was captured on a server carrying `@pfm-theme-generation`; a server the theme never reached, one born after an earlier off, keeps its own styles), unsets the prior options and every `@pfm-*` option, the hook's `@pfm-state` included; RESTORED with the server count.

### surfaceHerdr

**Surface:** `herdr` — **File:** `~/.config/herdr/config.toml` — **Live reload:** `herdr server reload-config`

Render (`herdrRender`): a marker-fenced block `# >>> pfm theme · {palette} · gen {N} >>>` … `# <<< pfm theme <<<` holding the `[theme]` table (`name`, and for mode auto `auto_switch = true`, `dark_name`, `light_name`; otherwise `auto_switch = false` with the one side's built-in name) and Herdr's 19 custom tokens (`herdrTokens`): dark values in `[theme.custom]`, and for mode auto the light values in `[theme.custom.light]`; mode light writes the light values into `[theme.custom]`.

Install (`herdrInstall`): the block replaces the user's existing `[theme]` and `[theme.*]` tables (first `[theme]` header through the last theme section's last content line); with none it is appended. A root-level `theme` key, or theme tables split around another table, is an ERROR naming the line. The span replaced (or text appended) is snapshotted in `state.herdr`, with a backup beside the file. A missing config is created when `herdr` is on PATH, else ABSENT.

Reload: a Herdr server that is not running, or `herdr` not on PATH, gives APPLIED with `applies when Herdr starts`; any other reload failure is an ERROR and stays `due`.

Read-back: finds the block, requires its text to equal the render and its header generation to equal the state's; a missing block or file is DRIFT.

Off: puts the replaced span back (or removes the appended text; deletes a file the sync created if it is empty again), reloads Herdr; RESTORED.

### surfaceGhostty

**Surface:** `ghostty` — **Files:** the first existing of `~/Library/Application Support/com.mitchellh.ghostty/{config.ghostty,config}` and `~/.config/ghostty/{config.ghostty,config}` — **Live reload:** `SIGUSR2`

Render (`ghosttyRender`): two lines appended to the user's config (`# pfm theme (managed: pfm theme off removes these two lines)` and `config-file = ?{config dir}/pfm-theme.ghostty`, absolute, so a config kept as a symlink into another directory still finds the include), and three files beside it: `pfm-theme.ghostty` (`# pfm theme · {palette} · gen {N} · mode {mode}` plus `theme = light:{file},dark:{file}` for auto, or the one side's file) and `pfm-theme/{palette}-dark`, `pfm-theme/{palette}-light` (background, foreground, cursor, selection, `palette = {0-15}=…`). Files of a palette no longer in force under `pfm-theme/` are deleted. The user's config is backed up beside itself and the appended text snapshotted in `state.ghostty`.

Reload: `ghostty +validate-config` runs first, with the first binary found: a running Ghostty's, `/Applications/Ghostty.app/Contents/MacOS/ghostty`, `~/Applications/Ghostty.app/Contents/MacOS/ghostty`, then `ghostty` on PATH. None found: the row stands on the file read-back alone, with the detail `not validated: Ghostty binary not found (tried …)`, never ERROR and never a re-apply. A found binary that rejects the config or will not run is an ERROR. Then `SIGUSR2` goes to each running Ghostty main process (found via `ps -axo pid=,args=`: the app binary alone, no arguments, and `ps -o comm= -p {pid}`, the executable's path, equal to that binary), after re-reading that pid's args and executable immediately before the kill; the binary "a running Ghostty's" names in the validation order above is that of such a process. No process: APPLIED with `applies when Ghostty starts`.

Read-back: the absolute include line present, the three owned files equal to the render, and the config validates; otherwise DRIFT (or ERROR when validation cannot run). No config file: ABSENT, listing the paths looked for; a candidate that cannot be looked at (a stat failure other than not-exist) is ERROR naming it, and so is a stale `pfm-theme/` file the apply cannot list or remove.

Off: removes the appended text (or the two lines wherever the user moved them) and any legacy relative `config-file = ?pfm-theme.ghostty` line an earlier pfm wrote, deletes `pfm-theme.ghostty` and `pfm-theme/`, signals Ghostty; RESTORED.

### surfaceVscode

**Surface:** `vscode` — **Contract:** `current.json` — **Live reload:** the pfm VS Code extension, `theme.js`, started in `activate`

Pfm never edits VS Code settings itself. It writes the contract `current.json` (`vscodeContract`: `enabled`, `generation`, `palette`, `title`, `mode`, `appearance`, `colors`; the colours are `terminal.background`, `terminal.foreground`, `terminalCursor.foreground`, `terminal.selectionBackground`, `terminal.tab.activeBorder` and the 16 `terminal.ansi*` keys). Before the first set it copies each existing `settings.json` of `Code`, `Code - Insiders`, `VSCodium` and `Cursor` (`~/Library/Application Support/{product}/User/`) beside itself. None present: ABSENT.

The extension polls the contract every 2000 ms (`professor.theme.stateDir` overrides the directory; the setting is `machine`-scoped and only its user value is read, so no workspace can point it elsewhere), merges the contract's terminal colours (`terminal.*` and `terminalCursor.*` keys with hex values; any other key or value is dropped) into the user's global `workbench.colorCustomizations` touching no other key, and writes `vscode-ack.json` (`generation`, `appliedAt`, `keys`, `error`). A generation applies once: another window or a restart finds it recorded in `vscode-applied.json` and skips it, so a colour the user changed by hand since stays theirs until the next `set` or `sync`. Each key set is recorded with the value it replaced; a key the next generation drops, or every key when the contract is `enabled: false`, goes back to its prior value unless the user changed it since. Windows serialise on the `vscode-apply.lock` directory (stale after 30 s). With no contract file the extension writes nothing.

### vscodeVerdict

Read-back is ABSENT when no VS Code `User/settings.json` exists, and ERROR naming the path when one cannot be looked at. It judges the ack against the state's generation, in order: an ack with `error` is ERROR (`extension reported: …`); an ack at the generation is IN-SYNC; no VS Code process running is PENDING (`applies when VS Code starts`); a contract younger than 10 s (`vscodeGrace`) is PENDING (`waiting for the extension to apply generation {N}`); a stale ack is DRIFT; no ack is DRIFT. A contract that differs from the render is DRIFT.

Off: writes a disabled contract (`enabled` false, no colours, bumped generation); the row reads APPLIED with `the extension clears the colours when it next reads current.json`, and the snapshot is dropped.

### surfaceFollower

**Surface:** `follower` — **File:** `~/Library/LaunchAgents/com.professor.pfm.theme.plist` — **Live reload:** `launchctl`

Only mode auto needs it: the agent runs `~/.local/bin/pfm theme sync` when macOS rewrites `~/Library/Preferences/.GlobalPreferences.plist` (`WatchPaths`), at load (`RunAtLoad`) and every 60 s (`StartInterval`), logging to `follow.log`, with `EnvironmentVariables` `PATH` set to `paths.ServicePath` — the one search path every pfm service runs on; launchd's bare default (`/usr/bin:/bin:/usr/sbin:/sbin`) holds neither tmux nor herdr. Apply writes the plist, which marks its reload `due`, and `launchctl bootstrap gui/{uid}` loads it (booting out first when the agent is loaded and the plist changed or a reload is `due`); only a successful bootstrap clears `due`, so a failed `print`, `bootout` or `bootstrap` leaves the reload owed to the next run. With mode `dark` or `light` the agent is retired and the row is ABSENT (`mode {mode}: the appearance follower is not needed`).

The agent never reloads itself: inside its own job (`XPC_SERVICE_NAME`, which themecmd passes as `Env.LaunchdJob`, equals `com.professor.pfm.theme`), a `bootout` would make launchd stop the running sync before `bootstrap`, leaving no agent. There a changed plist is written, the follower is marked `due`, and the row is ERROR (`launch agent plist rewritten; the agent cannot reload itself from inside launchd: run `pfm theme sync` in a shell`); the next run outside launchd boots it out, bootstraps it and clears `due`. An agent that is not loaded is bootstrapped even inside the job, since no bootout is needed. Retiring the agent for mode `dark` or `light` clears `due`.

Read-back: the plist equals the render and `launchctl print` finds it loaded, and no reload is `due`; a missing, differing or unloaded agent is DRIFT, and a `due` reload is ERROR (`launch agent plist rewritten but the loaded agent was not reloaded: run `pfm theme sync` in a shell`). The row carries no generation (`-`). Off: `launchctl bootout`, delete the plist; RESTORED, or ABSENT when none was installed.

## tmux doors

Three doors put the theme on a chat tmux server outside `set`/`sync`. Each is a no-op while `themesync.SyncOn` is false, so a pfm that never ran `pfm theme set` behaves as before.

### themeBornServer

`spawn.TmuxSpawner` calls it after configuring a newly created chat server: `themesync.ApplyTmuxServer` puts the current theme on that socket, capturing each style's prior first; it reads the state and applies under the theme lock, so it never lands after an `off` that already restored the server. Failure is logged and the spawn stands; a chat opens even when its colours could not be set.

### ConvergeTmux

`pfm name-sync --apply` calls `themecmd.ConvergeTmux` over the live chat sockets, so a server born before `set`, or one whose options a restart reset, joins the sync. It prints `tmux theme applied: {n}`; a server that refuses is named on stderr (`pfm name-sync: tmux theme {socket}: …`) and counted in `tmux options unverified`, which feeds name-sync's exit code. A preview (no `--apply`) does not run it.

### themeState

The `pfm internal herdr-state` hook, with the sync on, sets `@pfm-state` on the chat's own tmux server (`themesync.TmuxStateOption`; a pfm chat server only, so an engine in the user's own tmux leaves that server unmarked) from each event: `working`, `blocked` or `idle` as the Herdr plan reports it, except the idle event (a finished turn) which writes `done`. A session end (a release; not `/clear` or resume) unsets `@pfm-state` (`themesync.TmuxStateClear`), so a server that outlives its engine shows no agent state. Events that change no state (a compaction restart, a tmux-fired verb) write nothing. Failure is logged and the Herdr report goes on. The option is what `tmuxStateFormat` reads to colour the window and the active pane border.

## Install hold

### HoldsClaudeTheme

`themesync.HoldsClaudeTheme(stateDir, target)` is true when the sync is on and `state.claude` holds `target` (compared as a cleaned path and by resolved symlink; an unreadable state also holds). `pfm install` calls it per Claude theme file and, when held, reports `theme {name} held by pfm theme; pfm theme off restores it` and writes nothing, since `set` renders into that file and `off` restores its snapshot.

`pfm uninstall` with the sync on (`themesync.SyncOn`) runs `pfm theme off` (`themesync.Off`: every surface restored, `com.professor.pfm.theme` booted out, the state disabled) before it removes the themes; its plan pass prints `would run pfm theme off`, and an off that fails, or leaves a surface ERROR, stops the uninstall with `uninstall: pfm theme off: …`.
