# Brief: plugin-solvable Claude Code issues — one batch

Goal: from ONE batch of GitHub issues of anthropics/claude-code, keep every issue a Claude Code PLUGIN could solve or substantially work around, cluster the kept issues into themes, and write the clusters as JSON.

## Inputs
- Your batch: plugin-demands/batches/{BATCH}.tsv — TSV, header row; columns: number, state (open | not_planned = Anthropic closed it as not planned), thumbs_up, reactions (total incl. thumbs_up), comments, labels, year, title, body_head (first 200 chars; empty for zero-engagement issues).
- What a plugin can and cannot do: .claude/skills/plugin-builder/SKILL.md (read it fully first; references/*.md beside it for detail when a call is close).

## The filter
Keep an issue when a plugin (skills, commands, agents, output styles, settings hooks, MCP/LSP servers, bin/ executables, monitors, themes, statusline via settings, or experimental function hooks) could deliver what the reporter wants, or a workaround good enough that the reporter would install it. Drop: crashes/regressions inside Claude Code's own binary, auth/billing/account/server-side problems, model-quality complaints no prompt/hook can fix, desktop/web app UI a plugin cannot touch, install failures. When a feature request is only reachable via function hooks, keep it and mark layer "function-hooks". Duplicate-labeled issues count: they are demand signal, fold them into the cluster.

## Clustering
One cluster = one plugin someone could build. Merge issues asking for the same capability even when worded differently; split when two asks need different plugins. Aim for tight clusters (typically 20–120 per batch); singletons are allowed when demand is real.

## Output — write BOTH files, nothing else
1. plugin-demands/out/{BATCH}.json — a JSON array, one object per cluster:
   {"theme": "short name", "ask": "what users want, one sentence", "plugin": "the plugin sketch, 1–2 sentences, naming the layer mechanics", "layer": "skill|agent|command|settings-hook|mcp|lsp|statusline|output-style|monitor|bin|function-hooks|mixed", "feasibility": "full|workaround", "issues": [numbers], "thumbs_up": sum, "reactions": sum, "comments": sum, "open": count of open issues, "top_issue": number with most reactions}
   Sums computed from the TSV columns (compute with a script, never estimate).
2. plugin-demands/out/{BATCH}.stats.json — {"batch": "{BATCH}", "rows": N, "kept": N, "dropped": N, "clusters": N}.

Every row must be judged: kept + dropped = rows. Read the file in chunks; keep a running notes file at plugin-demands/out/{BATCH}.notes.md so a compaction loses nothing. Validate the JSON parses before finishing. Do not edit anything outside plugin-demands/out/. If you cannot read the batch or the skill file, write {BATCH}.stats.json with {"error": "..."} and stop — never an empty array that looks like "nothing kept".

Final reply: one line — `DONE {BATCH} rows=N kept=N clusters=N` or `FAILED {BATCH}: cause`.
