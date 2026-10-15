# 2026-10-12 — host `check-templates.sh` exits GAP and runs nothing

Observed: on this host `check-templates.sh` exits `GAP install (PyYAML)` before any check, so two flights' template steps ran only through the fence gate or by hand (`rumdl check FILES`, `bash scripts/leak-check.sh --files FILES`).

Amend: .claude/scripts/dev.sh or the templates testing manual — name the GAP and the by-hand fallback, or provision PyYAML the way the fence does.
