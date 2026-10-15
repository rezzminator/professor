# 2026-09-25 — Claude Code refuses a sub-agent's REPORT.md

Observed: the reviewer benchmark's lead wrote `SANDBOX/REPORT.md` and got `Subagents should return findings as text, not write report files` — Claude Code 2.1.282 refuses any sub-agent Write whose basename matches `^(REPORT|SUMMARY|FINDINGS|ANALYSIS).*\.md$`. `reviewer` now writes `review.md`. `templates/project/commands/rnd.md` still has its `general-purpose` run agent write `REPORT.md` (lines 27, 39, 43, 49), so that deliverable can never land.

Amend: templates/project/commands/rnd.md — rename the run's REPORT.md to a name outside the refused pattern (e.g. `RESULT.md`) end to end; any prompt that names a sub-agent's output file keeps it outside that pattern.

Resolved: 2026-10-12 — moot: /rnd and templates/project/commands/rnd.md are removed from the blueprint
