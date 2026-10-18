# Themes

`sources.json` declares the Claude Code themes `pfm install` places into `~/.claude/themes/` and records in the install ownership ledger. `pfm uninstall` removes only owned, unmodified files; a locally edited theme is reported and left alone. The same holds at install for a recorded theme the manifest no longer lists: it is removed when unmodified, reported and left when edited.

- `source_fetched` — fetched at install from the theme's canonical public repo, always latest; the blueprint never vendors a copy.
- `bundled` — a profile: `{name}.json` beside the manifest, read from the source clone when the manifest is, downloaded from beside the release manifest otherwise.

## Profiles

A profile carries `name`, `base` (`dark`|`light`) and `overrides` (Claude Code UI colour keys to hex), and optionally `surfaces`, per-surface pins (`tmux`, `herdr`, `vscode`) that win over what `pfm theme` derives for those surfaces. Any other top-level key is refused: install skips that profile, naming its file.

Install writes two copies of a profile:

- `~/.claude/themes/{name}.json` — Claude Code's file: `name`, `base` and `overrides` alone. A profile without `surfaces` is copied byte for byte; with `surfaces` the three palette keys are re-encoded, so no pin reaches Claude. Claude Code reads only those three keys and ignores any other.
- `~/.local/share/pfm/install/themes/{name}.json` — the whole profile, pins included, for `pfm theme` to read.

Both are recorded in the ownership ledger. A byte-identical file already sitting at either path is adopted into the ledger; a differing one stays and is reported.

A profile with a manifest `base` naming a `source_fetched` theme is an overlay — only `name` and the differing `overrides` (and `surfaces`) — merged onto the fetched base at install, so the base is never copied here and cannot drift.

Bundled today: `hibiscus-night`, `lagoon`, `mirrorball`, `neon-sunset`, `ultrafunk` and `velvet-groove`, each a complete palette. Select one with `pfm theme use {name}`, `/theme` in a chat, or `"theme": "custom:{name}"` in an account's `settings.json`.

## Adding a profile

1. Drop `{name}.json` beside `sources.json`; `{name}` is a bare name, with no `/`, `\` or `:`.
2. Add its `bundled` entry to `sources.json`: `file`, `target` (`~/.claude/themes/{name}.json`), `activate`, `requires`.
3. Add its `{"curated": true}` row to `templates/refresh-map.json`.
4. Run `pfm install`. No Go change is needed.
