# Themes

`sources.json` declares the Claude Code themes `pfm install` places into `~/.claude/themes/` and records in the install ownership ledger (`pfm uninstall` removes only owned, unmodified files; a locally edited theme is reported and left alone).

- `source_fetched` — fetched at install from the theme's canonical public repo, always latest; the blueprint never vendors a copy.
- `bundled` — a `{name}.json` beside the manifest, read from the source clone when the manifest is, downloaded from beside the release manifest otherwise. A complete palette carries `name`, `base` (`dark`|`light`) and `overrides` (Claude Code UI colour keys to hex). With a manifest `base` naming a `source_fetched` theme, the file is an overlay — only `name` and the differing `overrides` — and the installer writes it merged onto the fetched base, so the base is never copied here and cannot drift.

Bundled today: three Tokyo Night overlays, one per fleet account medal, differing only in the input bar (`promptBorder` + `promptBorderShimmer`) — `professor-gold` (🥇 account 1), `professor-silver` (🥈 account 2), `professor-bronze` (🥉 account 3). Select per account with `"theme": "custom:professor-gold"` in that account's `settings.json`, or `/theme` in a chat.
