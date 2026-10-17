# 2026-10-10 — a preview binary handed over beside an installed one reads as "the change does not work"

Observed: the ls-picker flight gave the user `tmp/bin/pfm.dev ls` to preview; the user's second try was plain `pfm`, the old installed binary, and the run looked like the new design had failed (shortcuts still there, no menu). Diagnosis needed shell history plus the launch-memory row.

Amend: templates/global/commands/flights/ (the foreman's preview hand-off) — a preview command for a binary that also exists installed says in the same line that plain `{tool}` is still the old one until the landing installs it.
