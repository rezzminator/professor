# Theme sync

`pfm theme` reads Claude Code's `theme` setting, read-only, and renders it into the native format of every surface the user looks at: tmux, Herdr and VS Code. It reloads each live, reads each back, and stamps all of it with one generation number, so "in sync" is a fact the status matrix proves. The sync never writes a Claude file; only `pfm theme use` sets the `theme` key of each account's settings.json ([List and use](#list-and-use)). The engine is `pfm/internal/themesync`; the verb and the name-sync door are `pfm/internal/themecmd`; the tmux seam is `pfm/internal/themetmux`. It ships inert: nothing is read, written or reloaded until `pfm theme on` writes `state.json`.

## Contents

- [Verb](#verb)
- [Source](#source)
- [Derivation](#derivation)
- [Profile pins](#profile-pins)
  - [Profile check](#profile-check)
  - [VS Code colour themes](#vs-code-colour-themes)
- [State directory](#state-directory)
- [Engine](#engine)
- [Verdicts](#verdicts)
- [Surfaces](#surfaces)
- [tmux doors](#tmux-doors)
- [Uninstall](#uninstall)

## Verb

### RunVerb

`pfm theme [status|sync [--force]|on|off|list|use {name}|check [file…]] [--json]`. Bare `pfm theme` is `status`. `--json` prints the `themesync.Report` on `status`, `sync`, `on` and `off` (`list`, `use` and `check` print their own shapes); `help` prints the usage. Any other subcommand is exit 2 (`pfm theme: unknown subcommand "x"` plus the usage).

| Verb | Does |
| --- | --- |
| `status` | `themesync.ReadBack`: reads the source and every surface back; writes nothing, so a source that moved shows as DRIFT |
| `sync` | `themesync.Sync`: follows the source now; `--force` re-applies every surface |
| `on` | `themesync.On`: reads the source, writes `state.json`, installs the follower, applies every present surface; the first `on` snapshots each user file it edits |
| `off` | `themesync.Off`: restores every surface from its snapshot, boots the follower out and disables the sync |
| `list`, `use` | pick a theme ([List and use](#list-and-use)) |
| `check` | runs install's profile checks ([Profile check](#profile-check)); writes nothing |

Exit codes: `0` ok; `1` when any surface reads DRIFT or ERROR (`Report.OK`) or a run failed (error on stderr as `pfm theme: …`); `2` usage. PENDING, ABSENT, APPLIED, IN-SYNC, RESTORED and SOURCE never raise the exit code.

With the sync never turned on (no `state.json`, or `enabled` false), `status` and `sync` print `pfm theme: off — pfm theme on turns it on` and exit 0; `--json` prints a report with `enabled` false and an empty `surfaces` array. `off` always runs: with no `state.json` it touches nothing and prints the same line; with a disabled `state.json` it restores whatever an earlier half-finished run left.

### drawMatrix

Text output is a header `pfm theme · {value} ({title}) · {appearance} · gen {N}` (the title omitted when equal to the value; `pfm theme · off` after an off), one row per surface (`surface  VERDICT  g{N}|-  path  detail`; coloured only on a truecolor terminal without `NO_COLOR`), a proof line from `proofLine` (`in sync · {n} of {total} surfaces at gen {N}`, with `· {k} pending` and `· {k} absent` appended, or `OUT OF SYNC · {surface} {VERDICT}, …`), and the `lastFlip` line.

### flipLine

`last flip {RFC3339 UTC} {origin file}: tmux +{s} s · herdr +{s} s · vscode contract +{s} s[, ack +{s} s]`. The offsets are measured from the origin file's mtime to each surface's apply completion: the governing `settings.json`, or the custom theme file, whichever is newer; an appearance-only flip uses the mtime of `~/Library/Preferences/.GlobalPreferences.plist`. The `ack` offset is the VS Code extension's `appliedAt` minus the origin, shown once an ack for the stored generation exists. The measurement is stored in `state.json` `lastFlip` (`themesync.Flip`) and recorded by every apply that bumps the generation.

`themecmd.Main` builds the `themesync.Env`: `Home`, `StateDir` (`DefaultStateDir`), `PfmBinary` (`~/.local/bin/pfm`, the stable launcher), the `obs`-wrapped runner, `themetmux.AtDir(values.TmuxDir)` and `LaunchdJob` (`XPC_SERVICE_NAME`). `GOOS` stays empty, meaning `runtime.GOOS`: `darwin` takes the launch agent and the `~/Library/Application Support` VS Code dirs, every other platform the systemd follower and `paths.VSCodeSettingsCandidates`; a test sets it to serve the other platform.

### List and use

`pfm theme list [--json]` and `pfm theme use {name} [--json]` are the one door that picks a theme. Picking sets the `theme` key of every Claude account `settings.json`, which stays the only source of truth; one sync then runs and the follower chain repaints tmux, Herdr and VS Code. `use` is the only place pfm writes a Claude file, and it writes that one key through the installer's `EditSettings` (atomic rename through a symlink, mode kept, every other key and number spelling kept, key order sorted); `themesync` takes it injected as `SettingsEdit` because the installer imports `themesync`. Claude chats without a higher-precedence theme pin repaint live from that key; synced pfm launches omit the pin ([themeForLaunch](#themeforlaunch)).

- `themesync.ListThemes` lists the custom themes (the union of the `*.json` stems in every account's themes dir, title from the file's `name`, else the stem) then Claude's built-ins and `auto`, each sorted by name. `Missing` names each account (`~/…`) whose themes dir lacks a custom theme; an absent themes dir is no error, but every custom theme then lists that account. A themes dir that cannot be read (not a directory, a dangling link), a theme file that does not parse and a `settings.json` that cannot be read are `Errors` rows naming the path, never absence; a themes dir shared by symlink between accounts is read once. `Current` is the value of the account `govern` picks from the stored memory while the sync is on (none while it is off), `dark` for an absent key; list writes nothing and runs with the sync on or off.
- `themesync.UseTheme` resolves the name against an exact `Value` first (`custom:x`, a built-in id, `auto`), then a custom stem, so a stem equal to a built-in id needs `custom:`; an unknown name is an error listing the valid names. For each account it refuses the custom theme when the file is missing from that account's themes dir (the `Refused` row names the dir and the file), and refuses an account whose edit fails; the run goes on to the other accounts and errors only when none was written. Theme files are never copied between accounts: the themes dirs are the installer's, and `themesync` only reads them.
- Text output: `list` draws `NAME  VALUE  TITLE` rows, `*` ahead of the current one, `missing in {dirs}` after a title, then `ERROR {path}: {error}` per `Errors` row. `use` prints `wrote {path}` per written file and `refused {path}: {error}` on stderr per refusal, then runs `Sync` once when the sync is on and draws its matrix, or prints `theme sync is off: run pfm theme on to repaint tmux, herdr and VS Code`, and ends with `open chats without a pinned theme follow live; for pinned chats, enable theme sync and run pfm reload`.
- `--json`: `list` prints the `ThemeList` (`themes` and `errors` are `[]` when empty), `use` prints `{"use": UseResult, "report": Report|null}` (`null` when the sync is off or its run failed).
- Exit codes: `list` is `1` when `Errors` is non-empty, else `0`. `use` is `0` when every account was written and the sync report is OK; `1` on any refusal, a failed sync or a report with a DRIFT or ERROR surface, the cause on stderr in one readable line per cause (the VS Code extension shows it); `2` when the name is missing or more than one is given.

## Source

### accountSettings

The source is the `theme` key of each Claude account's `settings.json`: `~/.claude/settings.json` and `~/.cc/*/settings.json`, deduped by real path (so an account directory symlinked to another is one account). No `settings.json` at all is an error: `no Claude account settings.json under ~/.claude or ~/.cc`.

### resolveValue

A `theme` value is a built-in name (`dark`, `light`, `dark-daltonized`, `light-daltonized`, `dark-ansi`, `light-ansi`), `auto`, or `custom:{name}`. An absent key or an unknown name resolves to `dark`, Claude's own default branch.

- Built-ins are embedded from Claude Code 2.1.301 in `themesync/builtin.go` as `theme token colour` rows, only the tokens the derivation reads.
- `custom:{name}` reads `{account dir}/themes/{name}.json` (`{name, base, overrides}`); the `base` built-in (dark when absent) supplies every token the overrides do not. An unreadable or unparsable file is the error `custom theme {name}: {cause naming the file}`.
- `auto` resolves both sides (`dark` and `light`) and is `Applied.Auto`: Herdr gets both, tmux and VS Code take the side macOS reports (`DetectAppearance`: `defaults read -g AppleInterfaceStyle` printing `Dark` is dark, exit 1 with "does not exist" is light; any other result is an error, never a guessed side). Herdr and Claude both read the host terminal background, so they agree by construction; launchd can read no terminal.
- A non-auto theme holds its own side: `light*` built-ins and a custom theme whose base is light draw light.

`parseColor` reads `rgb(r, g, b)`, `#rgb`, `#rrggbb`, `ansi256(n)` (the xterm 256 table) and `ansi:{name}` (xterm's default 16: `black #000000 … whiteBright #ffffff`); anything else is an error naming the text.

An unreadable source is an `ERROR` source row and nothing else is written (`on` leaves the sync off).

### govern

With several accounts the global surfaces follow the account whose `theme` value changed most recently. `state.json` `accounts` remembers each account's last seen value. The governor is, in order: the account whose value differs from its remembered value (the most recently changed settings file when several do); else the previous governor while it still exists; else, with no memory, the first account when all agree, otherwise the account with the newest `settings.json` mtime. The account overlay (`promptBorder`, `promptBorderShimmer`) stays in Claude's files and marks the account in Claude's input bar only; no surface reads it.

## Derivation

### deriveVariant

`deriveVariant(name, tokens)` maps one resolved token set onto a `Variant` (one side). A missing or unparseable token is an error naming it.

| Variant field | Claude token |
| --- | --- |
| `Background` | `inverseText` |
| `Foreground`, `Cursor`, `SelectionForeground` | `text` |
| `SelectionBackground` | `selectionBg` |
| `Surface` | `userMessageBackground` |
| `Overlay` | `subtle` |
| `Muted` | `inactive` |
| `Accent` | `claude` |
| `Orange` | `orange_FOR_SUBAGENTS_ONLY` |
| `State.Working` | `blue_FOR_SUBAGENTS_ONLY` |
| `State.Blocked` | `yellow_FOR_SUBAGENTS_ONLY` |
| `State.Done` | `success` |
| `State.Idle` | `inactiveShimmer` |
| `State.Unknown` | `inactive` |
| `ANSI[0]` | `inverseText` |
| `ANSI[1..6]` and `ANSI[9..14]` | the `red`, `green`, `yellow`, `blue`, `purple`, `cyan` tokens, each `_FOR_SUBAGENTS_ONLY` |
| `ANSI[7]` and `ANSI[15]` | `text` |
| `ANSI[8]` | `inactive` |

### liftColor

The contrast lift runs inside `deriveVariant`, so every surface draws the one lifted hue. Each of the five `State` hues is held to a 3:1 contrast ratio (`stateContrast`) against `Background`, and `Muted` to 2.5:1 against `Surface` (`mutedContrast`). A colour already clear is returned byte-identical. Otherwise `liftColor` keeps hue and saturation and moves HSL lightness in 1% steps away from the background (darker when the background's relative luminance exceeds 0.1791, the point where black out-contrasts white; else lighter) and returns the first colour that clears the guard. A hue no lightness can lift is an error naming theme, token and ratio.

A second pass, `liftColorAll`, holds each state hue to the same 3:1 against every background Herdr's sidebar paints under it (`Variant.sidebarBackgrounds`, in priority order): `Surface` (`sidebar_bg`, `surface0`, `surface_dim`: what every sidebar row and mark sits on), `SelectionBackground` (`active_row_bg`, `selection_bg`: the focused row), `Background` (`panel_bg`) and `Overlay` (`surface1`: the hovered row). It keeps hue and saturation, searches lightness in 1% steps in both directions, and returns the colour that clears every background in the fewest steps (a tie goes away from `Surface`, the first background). When no lightness clears all of them (`dark-ansi`: a `#7f7f7f` surface, a `#0000ee` selection, a black background and a `#e5e5e5` overlay), the last background is dropped and the search repeats, down to `Surface` alone, so the sidebar's own background is the last one given up and a theme that always resolved still resolves; `dark-ansi` therefore holds its hues to the first three and gives the hover row up. A hue no lightness lifts against `Surface` alone is the same error.

Which hue becomes which state is the theme's own palette, never a hard-coded hex: working is the theme's blue, blocked its yellow, done its `success`, idle its `inactiveShimmer` and unknown its `inactive`. A loud theme whose blue sits under 3:1 on its raised overlay (the round-3 `ultrafunk` blue `#8292f7` on `#a63784`) gets that blue lightened until it clears, hue kept.

### herdrTokens

Herdr's 19 theme tokens come from the lifted variant: `accent` = `Accent`; `panel_bg` = `Background`; `sidebar_bg`, `surface0`, `surface_dim` = `Surface`; `active_row_bg`, `selection_bg` = `SelectionBackground`; `surface1` = `Overlay`; `overlay0` = `Muted`; `overlay1`, `subtext0`, `green` (idle) = `State.Idle`; `text` = `Foreground`; `mauve` = `ANSI[5]`; `yellow` (working) = `State.Working`; `red` (blocked) = `State.Blocked`; `blue`, `teal` (done) = `State.Done`; `peach` = `Orange`. `accent` stays the unlifted `Accent` while `yellow` carries the lifted working hue, so the two differ only where the lift moved working (`light-daltonized`). Agent states therefore wear the hues Claude's palette carries: working its blue, blocked its yellow, done `success`, idle `inactiveShimmer`; `State.Unknown` (`inactive`) has no Herdr theme token and reaches Herdr only through the sidebar row styles below. tmux's `@pfm-state-*` options follow `State` too, so a blocked pane border is the theme's yellow.

With `agent_panel_sort = "priority"` in the Herdr config, the sidebar orders blocked, done, working, idle, unknown (see [the Herdr reporter](../hooks/hooks.md#herdr-reporter)).

### Applied

`Applied{Value, Title, Auto, Side, Primary, Alternate, Pins}` is the derived theme in force, persisted in `state.json` `applied`, so a tmux server born later (`ApplyTmuxServer`) renders it with no source read. `Primary` is the dark side for `auto` and `Alternate` the light side; `Applied.variant(appearance)` picks the side for the single-variant surfaces (tmux, VS Code). `Pins` are the governing profile's surface pins ([Profile pins](#profile-pins)), so a pin change is a changed theme.

## Profile pins

A theme profile is the directory `templates/themes/{name}/`, holding exactly `themesync.ProfileFiles()`, the same 40 files for every theme: `theme.json` (`name`, `base`, optional `vscodeSemanticHighlighting`), `claude.json` (the `overrides`), `terminal.json`, `tmux.json`, `herdr.json`, `vscode/chrome.json`, `vscode/semantic.json`, one `vscode/workbench/{group}.json` per workbench group (`workbenchGroups`, `profile_groups.go`) and one `vscode/syntax/{family}.json` per syntax family (`syntaxFamilies`, `profile_syntax.go`), a part the theme does not set being `{}`. A syntax family file keys each `tokenColors` rule by its four-digit ordinal, its index in `tokenColors`, and holds the rules whose first scope selector's root scope the family names; `joinSyntax` reads the families back into one ordered list. `themesync.ReadProfileDir` (a source clone) and `themesync.AssembleProfile` (a release, file by file) assemble them into one profile document: Claude Code's palette (`name`, `base`, `overrides`) and an optional `surfaces` object of per-surface pins that win over what derive fills. Each file must be canonical (keys sorted, two-space indent, one trailing newline: what `jq -S --indent 2 .` writes) each workbench colour in the file its group names and each syntax rule in its family's file; a missing or extra file, a non-canonical one (naming the line), a misplaced colour (`colour X belongs in vscode/workbench/Y.json`), a misplaced syntax rule (`rule "0000" (first scope "comment") belongs in vscode/syntax/comment.json`), an ordinal not four digits, held twice or leaving a gap refuses the theme, and a `{name}.json` file directly in `templates/themes/` is refused as a single-file profile. The layout, the group table and the family table are `templates/themes/README.md` § Profiles. `pfm install` writes the palette alone into Claude's themes dir and the whole assembled profile, pins included, to `~/.local/share/pfm/install/themes/{name}.json` (`ProfilePath`); the follower reads the pins there. The settings.json `theme` value stays the only source of truth: `custom:{name}` selects the profile by name, any other value has no pins, and a profile is a dropped directory, never Go code.

```json
"surfaces": {
  "tmux": { "@host_accent": "#ff3ddc", "status-style": "bg=#3f0535,fg=#ff9ed6" },
  "herdr": { "accent": "#ff3ddc" },
  "vscode": {
    "colors": { "terminal.background": "#23031e" },
    "chrome": { "titleBar.activeBackground": "#ff3ddc" },
    "theme": { "colors": { "editorCursor.foreground": "#ff3ddc" }, "tokenColors": [], "semanticTokenColors": {} }
  }
}
```

A pin is keyed by the name its renderer emits: tmux by option name (`tmuxOptions`), Herdr by custom token (`herdrTokens`) and, under `surfaces.herdr.tabGlyphs`, by tab state (`herdr.TabGlyphKeys`: `working`, `idle`, `blocked`, `done`, `error`, `dead`, `unknown`; a profile without `done` keeps the default single 🟡 frame; each key a glyph string or an array of frames, `herdr.TabFrames`, checked by `herdr.ValidTabStateFrames` (frame content by `herdr.ValidTabFrames`, plus exactly one frame for every state but `blocked` and `error`, the only states that may animate; a violating profile fails the theme check naming the state and the rule); the tab status painter reads them from `state.json` on every paint through `HerdrTabGlyphs`, and they never reach the Herdr config block), VS Code by contract key, `colors` and `chrome` apart (`vscodeContract`). `renderedPinKeys` collects the key sets by rendering a built-in dark theme through those renderers, so a key a renderer gains is pinnable with no second list. `ParseProfile` runs `validatePins` and refuses, naming the surface and the key:

- a key its renderer does not emit, `vscode colors` and `vscode chrome` included, so a chrome key under `colors` is refused;
- the four tmux bookkeeping options, `@pfm-theme`, `@pfm-theme-generation`, `@pfm-theme-chips` and `@pfm-state-colour`, which the follower keeps for its own bookkeeping;
- an empty value; a tmux chip (`@host_accent`, `@p_*`), `@pfm-c-*` or `@pfm-state-{blocked,working,done,idle}` option or a Herdr token whose value `parseColor` rejects; a VS Code key whose value is not `#rgb`, `#rgba`, `#rrggbb` or `#rrggbbaa`. A tmux style option takes any non-empty string, tmux's own to judge.

Pins are stored canonical (`canonicalPins`): tmux and Herdr colours as `#rrggbb`, VS Code hex as written, empty maps dropped, nil when nothing is pinned, so `Applied` still equals its `state.json` round trip.

`resolveSource` reads `ProfilePath` after `resolveValue` and sets `Applied.Pins`. An absent profile file means no pins and renders as before. A file that exists but cannot be read, parsed or validated, or is not a regular file, is an `ERROR` source row naming the profile file, and nothing is written: pins never fall silently away. Each renderer replaces the value of the key a pin names and no value derived from it follows (pinning `@pfm-state-working` leaves `window-status-current-style`'s state format as derive built it); both sides of an `auto` theme take the same pins, Herdr in `[theme.custom]` and `[theme.custom.light]` alike. The read-back compares each surface against this same pinned render, so a pinned surface reads `IN-SYNC` and a reset one `DRIFT`.

`surfaces.vscode.theme` is the profile's own VS Code colour theme layer (`VSCodeTheme`: `colors`, `tokenColors`, `semanticTokenColors`, each optional). `validateVSCodeTheme` refuses a colour id outside VS Code's registry (`vscode_color_ids.txt`, a snapshot of every `registerColor` id and every built-in extension's `contributes.colors`), a value that is not hex, a token rule without a scope array or a style, and a malformed semantic selector, each naming where it sits. Its colours for the contract's keys pin those keys wherever `colors` and `chrome` do not (`foldThemePins`), so the terminal and chrome pfm paints over any theme match the profile's own theme.

`Applied.Pins` persists in `state.json`, so a tmux server born later renders the pins too, and a pin change is a changed `Applied`: it bumps the generation and applies every surface. The profile file joins `settings.json` and Claude's theme file as a candidate flip origin: the newest mtime of the three is the edit the latency is measured from.

### Profile check

`pfm theme check [dir…] [--json]` runs `installer.CheckProfile` on each named theme directory — the directory read and profile parse install runs, `RenderVSCodeTheme`, then `themesync.CheckVSCodeReadability` on the rendered theme, which install does not run — and writes nothing. The readability check holds the floors derive lifts to: `editor.foreground` at 7:1, and every `tokenColors` and `semanticTokenColors` foreground at 4.5:1 (comments, quotes and struck-through text 3.5:1), on the editor and the current line; and inputs, dropdowns, the side bar, the active tab, the active list row, the status and title bars, buttons, the suggest and hover widgets and the terminal at 4.5:1, each translucent colour measured over what it is drawn on; each place under its floor is named in the `FAIL` row. The selection is not measured: derive lifts its own syntax off it where a lightness can, and a profile's own rules answer for themselves there. A TextMate scope name is not checked against any grammar: a misspelled scope passes and colours nothing. With no directory it checks every theme directory `installer.SourceProfiles` finds in the recorded source clone's `templates/themes/`; no recorded clone, a directory that cannot be read or one holding no theme is exit 1 naming why, never an empty pass. Text output is one `ok  {label}  {uiTheme}  {n} colours · {n} token rules · {n} semantic  {path}` row per pass, `FAIL {error}` per refusal (the error names the file and the key or line), then `{n} of {m} profile(s) pass`; `--json` prints `{"profiles": [{path, ok, id, label, uiTheme, colors, tokenColors, semanticTokenColors, error}]}`. Exit `0` only when every profile passes, `2` on usage.

### VS Code colour themes

`pfm install` renders one VS Code colour theme per profile the ownership ledger records, from its whole installed copy, so an edit made after install is what VS Code shows, with `themesync.RenderVSCodeTheme`: `deriveVSCodeTheme` builds the whole theme from the palette — layered workbench backgrounds, every syntax role in the palette's hues lifted to its contrast floor (7:1 body text, 4.5:1 tokens, 3.5:1 comments against the editor background, the current line and the selection, the selection given up first when no lightness clears all three), body text lifted before any tier is mixed from it, the deepest layer (tabs, inputs, code blocks) taking `clawd_background` only when body text reads on it at 7:1, symbol icons in the colours of the code they stand for — `mergeVSCodeTheme` lays `surfaces.vscode.theme` over it (colours and semantic selectors by key, a token rule's scopes moving to the profile's rule), and every contract key is set to what the contract renders. A theme that will not render is skipped by name and path; the rest still render. Each is written to the extension's `themes/professor-{stem}.json` (`name` the label, `type` `dark` or `light`), `themes/index.json` maps each `{id, label, uiTheme, path, value}`, and the embedded `package.json`'s `"themes": []` marker is filled with `contributes.themes` entries (`id` `professor-{stem}`, `label` `Professor · {profile name}`, `uiTheme` `vs-dark` or `vs` from `base`), so the Color Theme picker lists them as one group. A rendered theme no longer installed is pruned once `package.json` stops naming it; `pfm uninstall` removes them all. VS Code reads `contributes.themes` at window start: a new or removed theme shows after a window reload.

## State directory

`~/.local/state/pfm/theme/` (`DefaultStateDir`):

| File | Holds |
| --- | --- |
| `state.json` | `enabled`, `appearance`, `generation`, `source`, `accounts`, `applied`, `lastFlip`, `lastLog`, the `herdr` span snapshot, the `vscode` backup map, `due` (surfaces whose live reload failed) and `offPending` (an off left a surface unrestored); written atomically, mode 0600 |
| `current.json` | the VS Code contract the extension watches |
| `vscode-ack.json` | the extension's acknowledgement of a generation |
| `vscode-applied.json` | the extension's record of each colour key it set with the value it replaced |
| `pfm-theme.lock` | the flock serialising `on`, `sync` and `off` |
| `backups/` | copies of edited user files kept by the state store |
| `follow.log` | stdout and stderr of the follower (its launch agent, or its systemd service) |

Retired fields of an older `state.json` are ignored on read and dropped on the next write. Edited user files that live elsewhere (Herdr's `config.toml`, VS Code `settings.json`) are backed up beside themselves as `{file}.pfm-theme-{YYYYMMDD-HHMMSS}` the first time the sync edits them. A user file that is a symlink (a dotfiles checkout) is written at the file it resolves to. The extension also uses a `vscode-apply.lock` directory here while it merges settings.

### SyncOn

`SyncOn(stateDir)` is the one gate every door calls: false when `state.json` does not exist, the stored `enabled` otherwise. A `state.json` that exists but cannot be read or parsed reports true, so a door errs toward running and surfacing the error.

## Engine

### converge

Resolves the source and brings every surface to it. It bumps `generation` and applies every surface when the resolved `Applied` differs from the stored one or the appearance changed (or the state is old-shaped); `on` and `sync --force` apply all. Otherwise it reads every surface back: nothing drifted writes nothing and rewrites no state, and drifted surfaces alone are re-applied. An apply that was deferred reads APPLIED with its note rather than a bare IN-SYNC. A surface whose live reload failed is recorded in `due`; it keeps reading ERROR (`live reload failed earlier; the next sync retries it.`) until a later reload succeeds.

### Off

Bumps the generation (when it was enabled), sets `enabled` false, runs every surface's restore, clears `due`, `source`, `accounts`, `applied` and `lastFlip`, sets `offPending` exactly when a restore row is ERROR, and persists. `state.json` stays, disabled. `RestorePending(stateDir)`, the uninstall's gate, is `SyncOn` plus `offPending` or a `herdr` or `vscode` snapshot left behind, so an off that failed on a tmux server or the follower, which keep no snapshot, runs again on the rerun.

### LogOnce

The follower's quiet output. `state.json` `lastLog` holds the last line a follower run printed; the same line on the next run stays silent, and a clean run clears it.

## Verdicts

`themesync.Verdict`, per `SurfaceResult` (`surface`, `verdict`, `generation`, `path`, `detail`). Row order: `claude`, `tmux`, `herdr`, `vscode`, `follower`.

| Verdict | Meaning |
| --- | --- |
| `SOURCE` | the `claude` row only: Claude Code's governing account, theme value and rendered title (`{value} · {title}`); never written (`off` reads `never written`) and not counted as a surface |
| `IN-SYNC` | the surface read back equals the render at the state's generation (`generation` is what the surface itself reports; 0 when it carries none) |
| `APPLIED` | written (and reloaded) this run, or the reload is deferred and `detail` says when it takes effect |
| `RESTORED` | `off` put the surface back as it was |
| `PENDING` | the surface applies later: VS Code closed, or the extension has not acknowledged the generation yet |
| `ABSENT` | the surface is not installed or not applicable; `detail` names what was looked for |
| `DRIFT` | the surface exists but differs from the render, or the extension's ack is stale |
| `ERROR` | the surface or the source could not be read or written; `detail` carries the cause |

The read-back is generation-stamped where the surface can carry a stamp: the tmux option `@pfm-theme-generation`, the Herdr block header `gen {N}`, the VS Code ack's `generation`.

## Surfaces

### themeForLaunch

**Source surface:** Claude Code — **Live reload:** Claude watches the account's `settings.json`; no `/theme` command is needed when no higher-precedence setting pins the theme.

While `~/.local/state/pfm/theme/state.json` has `enabled: true`, `claudelaunch.Render` (interactive, resume, launcher and query) and `RenderHeadless` omit the `theme` key from pfm's `--settings` payload. The account's `settings.json` then owns the live theme, including changes from the VS Code Color Theme picker through `pfm theme use`. `pfm config claude` keeps its knob table and reports the value as `unset`, source `theme sync owns it`.

With sync off or no state file, the configured machine/account theme and headless per-run theme are carried unchanged. An unreadable or malformed state logs its path and cause, keeps carrying the configured theme, and marks the config row `theme sync state unreadable; carrying pin`. A chat already launched with a pinned theme needs `pfm reload` while sync is enabled to drop that launch-time pin; enabling sync alone cannot remove a running process's flags. A saved native Claude recipe is rechecked at restart: `chat.resumeNativeRun` (restored pane repair and detached native restart) calls `claudelaunch.RefreshThemePin`, which with sync on deletes the `theme` key from the final pfm-generated `--settings` JSON of a saved canonical `Render` recipe (separate or inline payload; other keys keep their number spellings through `json.RawMessage`). Sync off or unreadable state keeps the saved pin, unreadable logging the same warning. Earlier raw caller `--settings` and settings filenames stay untouched. Caller-supplied higher-precedence settings remain the caller's responsibility.

### surfaceTmux

**Surface:** `tmux` — **Targets:** every live pfm chat server (sockets in the chat tmux directory) — **Live reload:** `set-option -g` takes effect at once

Render (`tmuxOptions`): 8 style options (`status-style`, `window-status-style`, `window-status-current-style`, `pane-border-style`, `pane-active-border-style`, `message-style`, `message-command-style`, `mode-style`), 8 chip options (`tmuxChipNames`, which the user's status formats read at draw time: `@host_accent` the accent, `@p_g0` the surface (the bar base `status-style` paints), `@p_g3` the background, `@p_fg` the foreground, `@p_fg1` and `@p_fg2` the muted colour, `@p_red` and `@p_amber` ANSI 1 and 3) and 14 user options: `@pfm-theme` (the Claude theme value), `@pfm-theme-generation`, `@pfm-theme-chips` (the chip marker, `1`), `@pfm-c-{bg,fg,surface,overlay,muted,accent}`, `@pfm-state-{blocked,working,done,idle}` and `@pfm-state-colour`. The current-window and active-border styles use `tmuxStateFormat`, a conditional format on `@pfm-state` that picks the state colour (`blocked`, `working`, `done`, else the idle colour).

Apply (`tmuxApplySocket`): probe liveness (`status-style`; a dead socket is an error naming it), capture each style's prior value as `@pfm-theme-prior-{option}` on a server not yet carrying `@pfm-theme-generation` (stored as `v:{value}` so an empty prior still reads as captured), capture each chip option's prior in the same `@pfm-theme-prior-{option}` mechanism on a server not yet carrying `@pfm-theme-chips` — independent of the generation marker, so a server themed before the chips existed gets them captured on its next sync — as `v:{value}`, or `u:` when the option was unset (a read that fails once the server proved alive), then set every option in one apply per server. A partial failure reports `{n} of {k} servers failed`.

Read-back: every option on every server against the render, a chip a user `tmux source-file` reset included (DRIFT, healed by the next sync with its prior kept); a value mismatch is DRIFT, an unreadable option ERROR; the generation reported is `@pfm-theme-generation`. No live server: ABSENT (`no live pfm chat servers`).

Off: restores each style to its captured prior (unsets it when none was captured on a server carrying `@pfm-theme-generation`) and each chip option likewise (`v:{value}` sets it back, `u:` unsets it, no prior unsets it only on a server carrying `@pfm-theme-chips` and leaves the user's value alone otherwise), unsets the prior options and every `@pfm-*` option, the hook's `@pfm-state` included; RESTORED with the server count.

### surfaceHerdr

**Surface:** `herdr` — **Files:** `~/.config/herdr/config.toml`, `~/.config/herdr/pfm-indicators/herdr-plugin.toml` — **Live reload:** `herdr server reload-config`

Render (`herdrRender`): a marker-fenced block `# >>> pfm theme · {value, spaces to -} · gen {N} >>>` … `# <<< pfm theme <<<` holding the `[theme]` table and Herdr's 19 custom tokens (`herdrTokens`):

- `name` is `tokyo-night` (`tokyo-night-day` for a light side); all 19 tokens are overridden, so the base is inert.
- A non-auto theme writes `auto_switch = false` and the one side's tokens in `[theme.custom]`.
- `auto` writes `auto_switch = true`, `dark_name`, `light_name`, the dark tokens in `[theme.custom]` and the light tokens in `[theme.custom.light]`.

Install (`herdrInstall`): the block replaces the user's existing `[theme]` and `[theme.*]` tables (first `[theme]` header through the last theme section's last content line); with none it is appended. A root-level `theme` key, or theme tables split around another table, is an ERROR naming the line. The span replaced (or text appended) is snapshotted in `state.herdr`, with a backup beside the file. A missing config is created when `herdr` is on PATH, else ABSENT.

Sidebar row styles (`herdr_rows.go`): outside the pfm block, inside the `[ui.sidebar]` and `[ui.sidebar.*]` tables, every row entry that names an indicator token (`herdr.IndicatorTokens`: `pfm_working`, `pfm_blocked`, `pfm_done`, `pfm_unknown`) is rewritten to the one canonical inline table `{ token = "$pfm_X", fg = "#hex" }`: a bare array element exactly `"$pfm_X"`, or an inline table with no nested braces whose `token` is `"$pfm_X"` (its other keys are dropped). The hex is the state's hue from the applied theme on the side the appearance shows (`Applied.variant(Appearance)`, so a light auto theme gets its light-lifted hues): `pfm_working` = `State.Working`, `pfm_blocked` = `State.Blocked`, `pfm_done` = `State.Done`, `pfm_unknown` = `State.Unknown`. The scan reads TOML as TOML (comments, basic, literal and multi-line strings, brackets, inline tables), so a token named in a comment or inside a string, a table outside the sidebar tables and the pfm block are left byte-for-byte; a string that never closes is an ERROR naming its line. A file whose rows name no `$pfm_` token is untouched and no drift. Every apply restyles, so a theme change moves the hexes with the generation.

Plugin (`herdr_plugin.go`): the manifest `~/.config/herdr/pfm-indicators/herdr-plugin.toml` carries the id `herdr.IndicatorPluginID`, a name and version, `min_herdr_version = "0.9.5"`, `platforms = ["macos", "linux"]`, one `[[startup]]` and one `[[events]]` per `herdr.IndicatorEvents()`, each running `{pfm} internal herdr-indicators`. This is also the sole-chat tab-status monitor (`chat.herdrTabStatus`); it uses the same socket singleton and does not hook its own `tab.renamed` events. It is written when it differs, then linked with `herdr plugin link {dir}` when `~/.config/herdr/plugins.json` lacks the id, or when the manifest was just rewritten, so the registry entry is taken again from the manifest on disk (Herdr re-reads every linked manifest before it runs the hooks). Herdr 0.9.5 links with no server up by writing `plugins.json` itself; `herdr` off PATH is a note `indicator plugin applies when Herdr is running`, and read-back says DRIFT `is not linked in Herdr` only while `herdr` is on PATH. Any other link failure is an ERROR naming the command and Herdr's text. The plugin step runs after the due reload, so its failure never withholds a reload.

Reload: a Herdr server that is not running, or `herdr` not on PATH, gives APPLIED with `applies when Herdr starts`; any other reload failure is an ERROR and stays `due`.

Read-back: finds the block, requires its text to equal the render and its header generation to equal the state's; a missing block or file is DRIFT. Then the sidebar rows must equal their restyle (DRIFT `sidebar row pfm token styles differ`; a row naming a token the applied theme has no hue for, a state written before that hue existed, is DRIFT naming it, healed by the next apply), the plugin manifest must equal its render (DRIFT `manifest missing` or `differs`) and, with `herdr` on PATH, `plugins.json` must carry the plugin id (DRIFT `is not linked in Herdr`).

Off: puts the replaced span back (or removes the appended text; deletes a file the sync created if it is empty again), turns every styled row entry (an inline table of exactly `token` and `fg`) back to its bare `"$pfm_X"` string, also when the block is already gone, runs `herdr plugin unlink {id}` for a linked plugin, removes the manifest and its directory, clears the indicator tokens on every Herdr server a painter recorded (`herdr.ClearIndicators`, [hooks](../hooks/hooks.md); each socket's stop marker, its body unique to this off, is written first under the socket's stop lock, so a running blinker stops at its next tick and a paint that began before the off writes nothing more, even while a failed clear keeps the record and the marker for the retry; each pane-addressed clear is written only after `call.ProveHerdrPane` proves the pane — a durable seat or a pfm tmux client in the pane's foreground (bridge) — an unproved pane is skipped while the other targets are still cleared, and a failure is an ERROR the next off retries), reloads Herdr; RESTORED. An unlink that cannot run for want of a server leaves the registry entry and says which command removes it; any other unlink failure is an ERROR. Restoring is not byte-exact for an entry that was an inline table before the first apply: it comes back bare.

### surfaceVscode

**Surface:** `vscode` — **Contract:** `current.json` — **Live reload:** the pfm VS Code extension, `theme.js`, started in `activate`

Pfm never edits VS Code settings itself. It writes the contract `current.json` (`enabled`, `generation`, `palette` — the wire key, now carrying the Claude theme value —, `title`, `mode` (`dark|light|auto`), `appearance`, `colors`, `chrome`; the colours are `terminal.background`, `terminal.foreground`, `terminalCursor.foreground`, `terminal.selectionBackground`, `terminal.tab.activeBorder` and the 16 `terminal.ansi*` keys; `chrome` is the 27 window-chrome keys, derived from the variant: Surface paints `titleBar.activeBackground`, `activityBar.activeBackground`, `activityBarTop.activeBackground` and `statusBar.background`; Foreground the active title, activity-bar, command-center and status-bar foregrounds; Background the inactive title and status-bar backgrounds, `activityBar.background`, `activityBarTop.background`, `activityBarBadge.foreground`, `statusBar.debuggingForeground` and `statusBarItem.remoteForeground`; Muted the three inactive foregrounds; Accent both activity-bar active borders, `activityBarBadge.background`, `sash.hoverBorder`, `statusBar.debuggingBackground` and `statusBarItem.remoteBackground`; Overlay `commandCenter.border` and `statusBarItem.hoverBackground`. They are exactly the keys Peacock paints in a local workspace, so none stays Peacock's colour. `chrome` is a field apart from `colors` because the extension writes `colors` to the Global settings and `chrome` only to a local window's Workspace settings). Before the first `on` it copies each existing `settings.json` beside itself: on macOS those of `Code`, `Code - Insiders`, `VSCodium` and `Cursor` (`~/Library/Application Support/{product}/User/`), elsewhere every `paths.VSCodeSettingsCandidates` file, the one lookup `pfm install --vscode` uses (`Code`, `Code - Insiders` and `VSCodium` under `$XDG_CONFIG_HOME`, else `~/.config`; each VS Code Server's `data/Machine/settings.json`, the file a Remote-SSH window on the host reads; a portable install's under `VSCODE_PORTABLE`). None present, and outside macOS no `~/.vscode-server` or `~/.vscode-server-insiders` either (a server that never saved a Remote setting has no Machine `settings.json`, yet its extension host reads the contract): ABSENT, naming every path looked at.

The extension polls the contract every 2000 ms (`professor.theme.stateDir` overrides the directory; the setting is `machine`-scoped and only its user value is read, so no workspace can point it elsewhere), merges the contract's terminal colours (`terminal.*` and `terminalCursor.*` keys with hex values; any other key or value is dropped) into the user's global `workbench.colorCustomizations` touching no other key, and writes `vscode-ack.json` (`generation`, `appliedAt`, `keys`, `error`, `chrome`). A generation applies once: another window or a restart finds it recorded in `vscode-applied.json` and skips it, so a colour the user changed by hand since stays theirs until the next generation. Each key set is recorded with the value it replaced; a key the next generation drops, or every key when the contract is `enabled: false`, goes back to its prior value unless the user changed it since. Windows serialise on the `vscode-apply.lock` directory (stale after 30 s). With no contract file the extension writes nothing. The contract's `chrome` goes only to a local window on a saved workspace (`env.remoteName` undefined and `workspaceFile` a `file` URI), into that workspace's own `workbench.colorCustomizations`: the Workspace layer beats Global, and it is the layer Peacock writes. Only keys under `titleBar.`, `activityBar.`, `activityBarTop.`, `activityBarBadge.`, `commandCenter.`, `sash.`, `statusBar.` and `statusBarItem.` with hex values are written, and every other key of that workspace's customizations (Peacock's, the user's, `[Theme]` blocks) stays. A remote window, a folder-only window (its Workspace layer is a repo's `.vscode/settings.json`) and an untitled workspace get no chrome and add no ack entry. The record is `vscode-chrome.json`, `{workspace file: {generation, keys: {key: {value, prior}}}}` under the same lock, with its own once-per-generation guard per workspace: a window opening a workspace not yet painted at the generation paints it even when the Global colours are done, and the restore rules are the Global ones (an emptied layer drops the setting, an emptied record is removed). The ack's `chrome` map is `{workspace file: {generation, keys, error}}`; each window replaces its own entry and keeps the others' (read and written under the lock), and a Workspace write failure is that entry's `error`, never the top-level one. Peacock (`johnpapa.vscode-peacock`) rewrites its managed keys in the Workspace layer at every window start except those in `peacock.excludedSettings`, so with Peacock installed and a wanted chrome key missing from that setting the extension writes no chrome, advances no record and sets the entry's error `Peacock repaints {n} of {m} chrome keys at every window start: list them in this workspace's peacock.excludedSettings`; an edit of that setting re-runs the sync, and restoring (`enabled: false`) is never blocked. The extension's `Professor: Choose Theme` command (`choose.js`; palette entry shown by `!remoteName || professor.pfmHere`, the handler refusing before it runs anything where `pfmHere` fails) lists with `pfm theme list --json` in a quick pick, current theme first, and sets the pick with `pfm theme use {name}`, run by execFile from `~/.local/bin/pfm`, so the Claude source theme changes and the usual sync follows; the extension writes no setting for it, and a failure of either run is shown with pfm's stderr.

The VS Code Color Theme picker is a second door to `pfm theme use` (`native.js`), in a window pfm runs for: `choose.js` `pfmHere` holds in a local window (`env.remoteName` undefined), and in a remote window only when the extension host runs on the remote side (`extensionKind` Workspace) and finds an executable `~/.local/bin/pfm` there, as on a Linux host driven through Remote-SSH, where pfm, Claude Code, tmux and Herdr all live. Activation sets the `professor.pfmHere` context to it. Any other remote window, served from the UI side or whose host has no pfm, does neither direction, so its look stays its workspace's. Its one map is the install-rendered `themes/index.json` ([VS Code colour themes](#vs-code-colour-themes)); an unreadable index is logged and treated as no Professor themes, the rest of the extension running on.

- Native → pfm: a `workbench.colorTheme` change to an indexed id, in the focused window, runs `pfm theme use {value}` (execFile, the same runner and 30 s timeout as `choose.js`), its success a status-bar message and its failure an error naming the theme and pfm's stderr. A pick whose value is already the contract's `palette` runs nothing. Picks run one at a time, each re-reading the setting, so the last pick wins. An id outside the index is the user's own theme, left alone; terminal colours and chrome still follow pfm over it.
- pfm → native: after each applied contract, a `palette` that differs from the one recorded switches the Global `workbench.colorTheme` to its Professor theme; a palette with no Professor theme (a built-in Claude theme) records and switches nothing.
- Loop guard: `{stateDir}/vscode-native.json`, `{palette, id}`, the last palette followed, written atomically. A pick records its palette before pfm runs, so the contract pfm then writes is already followed; a resync of the same palette at a new generation never takes a non-Professor theme the user chose away. The first contract the extension ever sees (no record) is recorded, not followed, so installing the extension never switches the user's theme.

### vscodeVerdict

Read-back is ABSENT when none of those settings files exists (nor, outside macOS, a VS Code Server root), and ERROR naming the path when one cannot be looked at. It judges the ack against the state's generation, in order:

- an ack with `error` is ERROR (`extension reported: …`);
- an ack whose `chrome` map holds an entry with a non-empty `error` is ERROR (`window chrome {workspace file name}: {error}`, entries sorted by path and joined with `; `), so a chrome failure the extension acknowledged never reads IN-SYNC;
- an ack at the generation is IN-SYNC, its detail `window chrome in {n} local workspace(s)` counting the `chrome` entries at the generation without an error, empty when there are none;
- no VS Code process running is PENDING (`applies when VS Code starts`); `ps -axo args=` is read for the macOS app bundles' main binary, elsewhere for a VS Code Server (`~/.vscode-server{,-insiders}/cli/servers/` or `/bin/`) or a desktop install (`/usr/share/code/code`, `/usr/share/code-insiders/code-insiders`, `/usr/share/codium/codium`, `/opt/visual-studio-code/code`, `/snap/code/`);
- a contract younger than 10 s (`vscodeGrace`) is PENDING (`waiting for the extension to apply generation {N}`);
- no ack ever, with VS Code running past the grace, is PENDING (`reload a VS Code window once: no Professor extension has acknowledged a theme yet ({stateDir}/vscode-ack.json absent)`);
- an ack for another generation past the grace is DRIFT.

A contract that differs from the render is DRIFT, a contract written before `chrome` existed included: the next sync rewrites it.

Off: writes a disabled contract (`enabled` false, no colours, no chrome, bumped generation); the row reads APPLIED with `the extension clears the colours when it next reads current.json`, and the snapshot is dropped.

### surfaceFollower

**Surface:** `follower` — **File:** `~/Library/LaunchAgents/com.professor.pfm.theme.plist` on macOS, `~/.config/systemd/user/pfm-theme.path` elsewhere — **Live reload:** `launchctl`, or `systemctl --user`

On macOS `pfm theme on` installs the launch agent `com.professor.pfm.theme`, which runs `pfm theme sync` through the stable launcher `~/.local/libexec/pfm/pfm-agent` (`internal/agentlauncher`: a fixed `exec` of `~/.local/bin/pfm`, shared by every pfm launch agent, because macOS re-registers an agent whose program is a rebuilt, ad-hoc-signed pfm and posts a background-activity advisory per build). Its triggers:

- `WatchPaths`: the real path of each account's `settings.json`, each distinct real `themes/` directory, and `~/Library/Preferences/.GlobalPreferences.plist` (macOS rewrites it on an appearance flip);
- `ThrottleInterval` 1 second;
- `StartInterval` 60 seconds, a backstop for a rename-replace the watch misses;
- `RunAtLoad`.

Output goes to `follow.log`, with `EnvironmentVariables` `PATH` set to `paths.ServicePath`, the one search path every pfm service runs on; launchd's bare default holds neither tmux nor herdr.

In its own job (`XPC_SERVICE_NAME`, which themecmd passes as `Env.LaunchdJob`, equals `com.professor.pfm.theme`) `sync` is quiet:

- A run that changed nothing and reads every surface IN-SYNC or PENDING prints nothing.
- A run that applied prints one stdout line, `theme sync gen {N}: {value} ({appearance}) from {settings path} · tmux {n} · herdr · vscode contract`.
- A failure prints `theme sync gen {N} failed: {surface VERDICT detail; …}` or `theme sync: {error}` on stderr, once per distinct failure (`LogOnce`), exit 1.

The agent never reloads itself: inside its own job a `bootout` would make launchd stop the running sync before `bootstrap`, leaving no agent. There a changed plist is written, the follower is marked `due`, and the row is ERROR (`launch agent plist rewritten; the agent cannot reload itself from inside launchd: run `pfm theme sync` in a shell`); the next run outside launchd boots it out, bootstraps it and clears `due`. An agent that is not loaded is bootstrapped even inside the job, since no bootout is needed. A failed `print`, `bootout` or `bootstrap` leaves the reload owed to the next run.

Read-back: the plist equals the render, the launcher is current, `launchctl print` finds it loaded and no reload is `due`; a missing, differing or unloaded agent is DRIFT, and a `due` reload is ERROR. The row carries no generation (`-`). Off: `launchctl bootout`, delete the plist; RESTORED, or ABSENT when none was installed.

Everywhere else the follower is a systemd user unit trio in `~/.config/systemd/user/`, beside the installer's `pfm-name-sync` and `pfm-schedule` units, written and removed by `pfm theme` alone (`follower_systemd.go`); no `~/Library` path is written and `launchctl` never runs:

- `pfm-theme.service`: `Type=oneshot`, `ExecStart` `{PfmBinary} theme sync` (no launcher: the stable-launcher reason is macOS's), `Environment` `PATH` = `paths.ServicePath` and `XPC_SERVICE_NAME=com.professor.pfm.theme`, so its sync is the quiet one above, stdout and stderr appended to `follow.log`;
- `pfm-theme.path`: one `PathModified=` per real account `settings.json` and distinct real `themes/` dir (no preferences file), `WantedBy=default.target`;
- `pfm-theme.timer`: `OnActiveSec=1s` (at start), `OnUnitInactiveSec=60s` (the backstop), `AccuracySec=1s`, `WantedBy=timers.target`.

A unit that differs from the render is rewritten and the follower marked `due`; with both triggers `is-active` and nothing due the row is APPLIED, otherwise `systemctl --user daemon-reload`, `enable pfm-theme.path pfm-theme.timer` and `restart` of both run, a failure staying due for the next run. A restart inside the follower's own service is safe: the running oneshot is not restarted. A plist an earlier pfm wrote on a non-macOS host is deleted. Read-back: each unit equals the render (else DRIFT, `systemd unit {name} is missing|differs from the render`), both triggers active (else DRIFT, `systemd units not active: {unit state}`), no reload `due` (else ERROR); a manager that answers no state per unit is ERROR, never inactive. Off: `daemon-reload`, `disable --now` of each trigger whose unit file is present (systemctl refuses the whole disable over one missing file), delete the three units, `daemon-reload`; RESTORED, or ABSENT (`no systemd units installed`) without asking the manager when no unit file exists.

## tmux doors

Three doors put the theme on a chat tmux server outside `on`/`sync`. Each is a no-op while `themesync.SyncOn` is false, so a pfm that never ran `pfm theme on` behaves as before.

### themeBornServer

`spawn.TmuxSpawner` calls it after configuring a newly created chat server: `themesync.ApplyTmuxServer` puts the stored `Applied` on that socket, capturing each style's prior first; it reads the state and applies under the theme lock, so it never lands after an `off` that already restored the server. Failure is logged and the spawn stands; a chat opens even when its colours could not be set.

### ConvergeTmux

`pfm name-sync --apply` calls `themecmd.ConvergeTmux` over the live chat sockets, so a server born before `on`, or one whose options a restart reset, joins the sync. It prints `tmux theme applied: {n}`; a server that refuses is named on stderr (`pfm name-sync: tmux theme {socket}: …`) and counted in `tmux options unverified`, which feeds name-sync's exit code. A preview (no `--apply`) does not run it.

### themeState

The `pfm internal herdr-state` hook, with the sync on, sets `@pfm-state` on the chat's own tmux server (`themesync.TmuxStateOption`; a pfm chat server only, so an engine in the user's own tmux leaves that server unmarked) from each event: `working`, `blocked` or `idle` as the Herdr plan reports it, except the idle event (a finished turn) which writes `done`. A session end (a release; not `/clear` or resume) unsets `@pfm-state` (`themesync.TmuxStateClear`), so a server that outlives its engine shows no agent state. Events that change no state write nothing. Failure is logged and the Herdr report goes on. The option is what `tmuxStateFormat` reads to colour the window and the active pane border.

## Uninstall

`pfm uninstall` with a restore pending (`themesync.RestorePending`) runs `pfm theme off` (`themesync.Off`: every surface restored, the follower removed, the state disabled) before it removes the themes; its plan pass prints `would run pfm theme off`, and an off that fails, or leaves a surface ERROR, stops the uninstall with `uninstall: pfm theme off: …`. `pfm install` writes the profile and Claude theme files and the extension's VS Code colour themes, and leaves a theme file the user edited after install in place (`locally modified; left in place`); the sync holds none of them.
