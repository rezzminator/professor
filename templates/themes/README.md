# Themes

Every subdirectory of this directory is one Professor theme, a profile, listed nowhere else. `pfm install` finds each by scanning the directory, in the source clone when the install runs from one, otherwise through GitHub's contents listing of `templates/themes/` at the release's ref, each file downloaded from there. It places each into `~/.claude/themes/` and records it in the install ownership ledger. `pfm uninstall` removes only owned, unmodified files; a locally edited theme is reported and left alone. The same holds at install for a recorded theme no longer here: it is removed when unmodified, reported and left when edited or still selected by an account.

## Profiles

A profile is one directory, `{name}/`, holding the whole theme on every surface in a fixed set of 40 files, the same for every theme (`themesync.ProfileFiles`):

```text
{name}/
  theme.json              name, base (dark|light), optional vscodeSemanticHighlighting
  claude.json             Claude Code's palette: UI colour key to hex (the profile's overrides)
  terminal.json           surfaces.vscode.colors: the terminal colours
  tmux.json               surfaces.tmux
  herdr.json              surfaces.herdr: custom tokens and tabGlyphs
  vscode/
    chrome.json           surfaces.vscode.chrome
    syntax/{family}.json  surfaces.vscode.theme.tokenColors, one file per family (§ Syntax families)
    semantic.json         surfaces.vscode.theme.semanticTokenColors
    workbench/{group}.json  surfaces.vscode.theme.colors, one file per group (§ Workbench groups)
```

- Every file is present; a part the theme does not set is `{}`. No other file or directory is allowed.
- A shipped theme file holds at most 450 lines, so each reads in one sitting; `profile_dir_test.go` (`maxThemeFileLines`) enforces it.
- Every file is canonical: keys sorted, two-space indent, one trailing newline. `jq -S --indent 2 . {file}` writes that form; a file in any other form is refused, naming the line.
- `name`, `base` and `claude.json` are Claude Code's palette; everything else is derived from it.
- Every other file is a surface's pins. A key it sets wins over what `pfm theme` derives; a key it omits stays derived:
  - `tmux.json`: tmux options by name (styles, `@host_accent` and the `@p_*` chips, `@pfm-c-*`, `@pfm-state-*`);
  - `herdr.json`: Herdr custom tokens, plus `tabGlyphs`, the Herdr tab status glyphs in `emoji` mode, by state: `working`, `idle`, `blocked`, `done`, `error`, `dead`, `unknown`. `done` is finished output the user has not seen: it holds until the tab's first focus, then turns `idle`. The colour carries the state, and only `blocked` and `error`, the states that need the user, may flash: each takes a glyph string (one still frame) or an array of 1 to 12 frames played in order, one per half-second, every painter on the same frame at the same moment. The steady states `working`, `done`, `idle`, `dead` and `unknown` never blink: each takes exactly one frame. The defaults are the still `working` 🔵, `done` 🟡, `idle` ⚪, `dead` ⬛ and `unknown` ❔, and the flashing `blocked` 🔴 ⭕ and `error` 🟥 ❌; a state a profile leaves unset takes its default. Each frame is non-empty, at most 8 code points, with no whitespace or control character; `pfm theme check` names the state and the index of a bad frame, refuses an empty array, and refuses a second frame on a steady state. The tab painter draws a switched theme's glyphs on its next paint; `text` mode keeps its `[W]`-style markers;
  - `terminal.json`: the terminal colours pfm paints into VS Code's Global settings;
  - `vscode/chrome.json`: the window chrome (window borders, title, activity and status bars) pfm paints into VS Code's Global settings and into each window's Workspace settings;
  - `vscode/workbench/*.json`, `vscode/syntax/*.json` and `vscode/semantic.json`: the profile's VS Code colour theme — workbench colours (any VS Code colour id), TextMate rules (`scope` always an array) and semantic styles (a bare hex for a foreground-only style, else `{foreground, bold, italic, underline, strikethrough}`). Each colour and semantic selector named replaces the derived one; each scope a rule styles moves to that rule; the rest of the derived theme stays.
- A key nothing reads is refused, naming the file and the key: an unknown key, a pin no renderer emits, a VS Code colour id outside VS Code's registry, a value that is not a colour, a workbench colour in another group's file (`colour X belongs in vscode/workbench/Y.json`), a syntax rule in another family's file or with a bad ordinal (§ Syntax families). A missing or extra file is refused too. Install skips that theme and installs the rest.

From `tokyo-night/`, the pink reference theme, `theme.json`:

```json
{
  "base": "dark",
  "name": "Tokyo Night · neon pink"
}
```

`herdr.json`, tokens trimmed:

```json
{
  "accent": "#ff3ddc",
  "panel_bg": "#23031e",
  "tabGlyphs": {
    "blocked": ["🚨", "🔴"],
    "dead": "🪫",
    "done": "⭐",
    "error": ["👾", "💥"],
    "idle": "🌑",
    "unknown": "🤖",
    "working": "🚀"
  },
  "text": "#ffb5e4"
}
```

`vscode/semantic.json`, trimmed:

```json
{
  "class": "#dc8aff",
  "comment": {
    "foreground": "#bc62ab",
    "italic": true
  }
}
```

### Workbench groups

A VS Code colour id belongs to the group whose prefix claims its first dotted segment: the longest prefix that equals the segment or that the segment continues at a camelCase boundary (`editor` claims `editorCursor`, not `editors`; `editorGutter` claims `editorGutter.background` over `editor`). `profile_groups.go` (`workbenchGroups`) holds the table; it is the whole rule.

| `vscode/workbench/` file | Prefixes |
| --- | --- |
| `activitybar.json` | `activity`, `modernActivityBar` |
| `base.json` | `actionBar`, `badge`, `banner`, `browser`, `chart`, `charts`, `contrastActiveBorder`, `contrastBorder`, `descriptionForeground`, `disabledForeground`, `errorForeground`, `focusBorder`, `foreground`, `icon`, `markdownAlert`, `modernSash`, `modernUI`, `profileBadge`, `profiles`, `progressBar`, `sash`, `scrollbar`, `selection`, `strongForeground`, `surface`, `text`, `toolbar`, `walkThrough`, `walkthrough`, `welcomePage`, `widget` |
| `chat.json` | `activeSessionView`, `agent`, `agents`, `chat`, `inactiveSessionView`, `inlineChat`, `interactive`, `mcpIcon` |
| `controls.json` | `button`, `checkbox`, `dropdown`, `extension`, `input`, `keybinding`, `radio`, `settings` |
| `debug-testing.json` | `debug`, `ports`, `problems`, `testing` |
| `diff-merge.json` | `diffEditor`, `merge`, `multiDiffEditor` |
| `editor.json` | `editor` |
| `editor-guides.json` | `editorBracket`, `editorIndentGuide` |
| `editor-gutter.json` | `editorGutter`, `editorMinimap`, `editorOverviewRuler`, `minimap` |
| `editor-widgets.json` | `commentsView`, `editorActionList`, `editorCommentsWidget`, `editorError`, `editorHint`, `editorHoverWidget`, `editorInfo`, `editorLightBulb`, `editorMarkerNavigation`, `editorSuggestWidget`, `editorWarning`, `editorWidget`, `inlineEdit`, `peekView`, `simpleFindWidget` |
| `notebook.json` | `notebook` |
| `notifications.json` | `notification`, `notifications` |
| `panel.json` | `modernPanel`, `outputView`, `panel` |
| `quick-input.json` | `pickerGroup`, `quickInput` |
| `sidebar-explorer.json` | `git`, `list`, `scmGraph`, `search`, `sideBar`, `tree` |
| `statusbar.json` | `statusBar` |
| `symbols.json` | `symbolIcon`, `terminalSymbolIcon` |
| `tabs.json` | `breadcrumb`, `editorGroup`, `modernEditorTab`, `modernTab`, `sideBySideEditor`, `tab` |
| `terminal.json` | `terminal` |
| `titlebar-menus.json` | `commandCenter`, `menu`, `menubar`, `titleBar`, `window` |

### Syntax families

`vscode/syntax/` holds the profile's TextMate rules (VS Code's `tokenColors`), one file per family, all 13 present in every theme. A rule belongs to the family naming the root scope, the first dotted segment, of the first word of its first `scope` selector (`comment.block.documentation variable` is `comment`); a rule whose root no family names, or with no scope, belongs in `other.json`. `profile_syntax.go` (`syntaxFamilies`) holds the table; it is the whole rule.

| `vscode/syntax/` file | Root scopes |
| --- | --- |
| `comment.json` | `comment` |
| `constant.json` | `constant` |
| `diagnostic.json` | `invalid`, `log`, `token` |
| `entity.json` | `entity` |
| `keyword.json` | `keyword`, `storage` |
| `markup.json` | `fenced_code`, `heading`, `markup`, `text` |
| `meta.json` | `meta` |
| `other.json` | every other root, and a rule with no scope |
| `punctuation.json` | `punctuation` |
| `source.json` | `source` |
| `string.json` | `string` |
| `support.json` | `support` |
| `variable.json` | `variable` |

Each file is an object keyed by the rule's ordinal, its index in `tokenColors` as four zero-padded digits (`"0000"`, `"0001"`, …), each value the rule `{name, scope, settings}`. Read across every family file in ordinal order, the rules are VS Code's ordered list, where a later rule wins over an earlier one of equal weight. A theme setting no rule of a family leaves its file `{}`. The ordinals run from `0000` without a gap, each held once; a new rule takes the next ordinal after the highest any family file holds, in its family's file, and a rule inserted mid-order renumbers every later ordinal. Install and `pfm theme check` refuse, naming the file and the rule, a rule in the wrong family's file (`vscode/syntax/meta.json: rule "0000" (first scope "comment") belongs in vscode/syntax/comment.json`), an ordinal not four digits, an ordinal held twice (`rule "0001" holds an ordinal vscode/syntax/comment.json already holds`) and a gap (`vscode/syntax: no rule holds ordinal "0002"`).

From `tokyo-night/`, `vscode/syntax/source.json`, trimmed:

```json
{
  "0033": {
    "name": "C-related Block Level Variables",
    "scope": [
      "source.cpp meta.block variable.other"
    ],
    "settings": {
      "foreground": "#ff5c8a"
    }
  }
}
```

### Install

Install assembles a theme's files into one profile document, `{name, base, overrides, surfaces}`, and writes two copies:

- `~/.claude/themes/{name}.json` — Claude Code's file: `name`, `base` and `overrides` alone, so no pin reaches Claude.
- `~/.local/share/pfm/install/themes/{name}.json` — the whole assembled profile, pins included, for `pfm theme` to read.

Both are recorded in the ownership ledger. A byte-identical file already sitting at either path is adopted into the ledger; a differing one stays and is reported. A `{name}.json` file directly in `templates/themes/` is refused as a single-file profile.

## VS Code

Install renders one complete VS Code colour theme per installed profile into the Professor extension: workbench, syntax and semantic colours derived from the palette, the profile's `vscode/workbench/`, `vscode/syntax/` and `semantic.json` laid over them, and the terminal and chrome keys set to what `pfm theme` paints. Preferences: Color Theme (Cmd+K Cmd+T) lists them together as `Professor · {name}`, light or dark from `base`. Picking one there runs `pfm theme use` for it, so every surface follows; `pfm theme use` or `/theme` elsewhere switches a local VS Code window to the matching Professor theme. A non-Professor colour theme the user picks stays. A remote window follows with its frame and terminal colours but never switches its full Color Theme; the picker's pfm integration and Professor chooser are local-only. A new or removed profile shows up after `pfm install` and a window reload.

Select a theme, `{name}` being its directory name, with `pfm theme use {name}`, `/theme` in a chat, the VS Code Color Theme picker, or `"theme": "custom:{name}"` in an account's `settings.json`.

## Adding a profile

1. Copy an existing theme's directory to `{name}/` and edit its files; `{name}` is a bare name, with no `/`, `\` or `:`. Keep all 40 files, each canonical (`jq -S --indent 2 .`), each workbench colour in its group's file, each syntax rule in its family's file under its ordinal.
2. Run `pfm theme check templates/themes/{name}` from the clone root: it runs every check install runs and renders the VS Code theme, writing nothing, and prints `ok` with the theme's label and counts or `FAIL` naming the file and the key or line (bare `pfm theme check` checks every theme in the source clone; `--json` prints each result).
3. Run `pfm install`, then reload VS Code's windows. No Go change, list entry or refresh-map row is needed.
