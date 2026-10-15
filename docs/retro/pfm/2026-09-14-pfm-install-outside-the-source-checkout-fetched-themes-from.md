# 2026-09-14 — `pfm install` outside the source checkout fetched themes from an unpublished release

- 2026-09-14 bundled-themes wave: `pfm install --yes` run from outside the source checkout fell back to the release manifest at `…/professor/0.78.0-alpha/templates/themes/sources.json` and 404'd (the alpha tag is never published), so the bundled palettes were "NOT installed" until re-run from `~/.professor`. `discoverSourceRepo()` is cwd-based while the recorded source clone is known to the installer — the theme loader should consult the recorded clone before the release URL, and an unpublished `-alpha` reference should say so rather than surface as a bare 404. Also: the preview row says `fetch theme X` for a bundled palette that is read from disk — label by kind.

Resolved: 2026-09-15 — pfm/internal/installer/themes.go
