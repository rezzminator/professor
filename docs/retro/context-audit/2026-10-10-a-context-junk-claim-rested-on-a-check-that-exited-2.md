# 2026-10-10 — a context-junk claim rested on a check that exited 2

Observed: the main loop's first context audit read `~/.claude.json` (absent; the active config is `$CLAUDE_CONFIG_DIR/.claude.json`), the chained step exited 2 with no FAILED line, and a row on the Chrome integration was reported as if the check had run. The buddy plugin caught it; the corrected check reversed half the row.

Amend: judgment — the rule exists (fleet prompt § Command execution: a chained step that fails says so on its own line); `/context-audit` step 3 now carries it for this workflow.
