# Themes

Every `*.json` file in this directory is one Professor theme, a profile, listed nowhere else. `pfm install` finds each by scanning the directory, in the source clone when the install runs from one, otherwise through GitHub's contents listing of `templates/themes/` at the release's ref, each downloaded from there. It places each into `~/.claude/themes/` and records it in the install ownership ledger. `pfm uninstall` removes only owned, unmodified files; a locally edited theme is reported and left alone. The same holds at install for a recorded theme no longer here: it is removed when unmodified, reported and left when edited or still selected by an account.

## Profiles

A profile is one file, `{name}.json`, and holds the whole theme on every surface:

```json
{
  "name": "Hibiscus Night · everything pink",
  "base": "dark",
  "overrides": { "background": "#1a0a12", "text": "#f5e6ee", "claude": "#ff2e93" },
  "surfaces": {
    "tmux": { "@host_accent": "#ff2e93" },
    "herdr": { "accent": "#ff2e93" },
    "vscode": {
      "colors": { "terminal.background": "#1a0a12" },
      "chrome": { "titleBar.activeBackground": "#2a0a1a" },
      "theme": {
        "colors": { "editorCursor.foreground": "#ff6ba7" },
        "tokenColors": [
          { "name": "types", "scope": ["entity.name.type"], "settings": { "foreground": "#ffd479" } }
        ],
        "semanticTokenColors": { "class": "#ffd479", "comment": { "foreground": "#a87d90", "italic": true } }
      }
    }
  }
}
```

- `name`, `base` (`dark`|`light`) and `overrides` (Claude Code UI colour keys to hex) are Claude Code's palette; everything else is derived from it.
- `surfaces` is optional, and so is every key under it. A key it sets wins over what `pfm theme` derives; a key it omits stays derived:
  - `tmux` — tmux options by name (styles, `@host_accent` and the `@p_*` chips, `@pfm-c-*`, `@pfm-state-*`);
  - `herdr` — Herdr custom tokens;
  - `vscode.colors` — the terminal colours pfm paints into VS Code's Global settings;
  - `vscode.chrome` — the window chrome (title, activity and status bars) pfm paints into a local workspace;
  - `vscode.theme` — the profile's VS Code colour theme: `colors` (any VS Code colour id), `tokenColors` (TextMate rules, `scope` always an array) and `semanticTokenColors` (a hex or `{foreground, bold, italic, underline, strikethrough}`). Each colour and semantic selector named replaces the derived one; each scope a rule styles moves to that rule; the rest of the derived theme stays.
- A key nothing reads is refused, naming the file and the key: an unknown top-level key, a pin no renderer emits, a VS Code colour id outside VS Code's registry, a value that is not a colour. Install skips that profile and installs the rest.

Install writes two copies of a profile:

- `~/.claude/themes/{name}.json` — Claude Code's file: `name`, `base` and `overrides` alone. A profile without `surfaces` is copied byte for byte; with `surfaces` the three palette keys are re-encoded, so no pin reaches Claude. Claude Code reads only those three keys and ignores any other.
- `~/.local/share/pfm/install/themes/{name}.json` — the whole profile, pins included, for `pfm theme` to read.

Both are recorded in the ownership ledger. A byte-identical file already sitting at either path is adopted into the ledger; a differing one stays and is reported.

## VS Code

Install renders one complete VS Code colour theme per installed profile into the Professor extension: workbench, syntax and semantic colours derived from the palette, `surfaces.vscode.theme` laid over them, and the terminal and chrome keys set to what `pfm theme` paints. Preferences: Color Theme (Cmd+K Cmd+T) lists them together as `Professor · {name}`, light or dark from `base`. Picking one there runs `pfm theme use` for it, so every surface follows; `pfm theme use` or `/theme` elsewhere switches a local VS Code window to the matching Professor theme. A non-Professor colour theme the user picks stays. A remote window never switches. A new or removed profile shows up after `pfm install` and a window reload.

Select a theme, `{name}` being its file name without `.json`, with `pfm theme use {name}`, `/theme` in a chat, the VS Code Color Theme picker, or `"theme": "custom:{name}"` in an account's `settings.json`.

## Adding a profile

1. Drop `{name}.json` in this directory; `{name}` is a bare name, with no `/`, `\` or `:`.
2. Run `pfm theme check {name}.json`: it runs every check install runs and renders the VS Code theme, writing nothing, and prints `ok` with the theme's label and counts or `FAIL` with the file and key (bare `pfm theme check` checks every profile in the source clone; `--json` prints each result).
3. Run `pfm install`, then reload VS Code's windows. No Go change, list entry or refresh-map row is needed.
