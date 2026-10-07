# Brief: merge the batch clusters into one ranked list of demanded Claude Code plugins

Input: plugin-demands/out/*.json (26 batches; one JSON array of clusters each — theme, ask, plugin, layer, feasibility, issues, thumbs_up, reactions, comments, open, top_issue). Batches were split by product area, so ONE plugin idea appears in several batches under different theme names. Raw issue rows for spot checks: plugin-demands/corpus.tsv (number, state, state_reason, thumbs_up, reactions, comments, labels, created, title, body_head). What a plugin can do: .claude/skills/plugin-builder/SKILL.md.

## Step 1 — canonicalize (your judgment)
Group every batch cluster into canonical plugins: one canonical plugin = one thing a person would build and publish. Merge same-capability clusters across batches; split a batch cluster only if it plainly bundles two plugins. Write plugin-demands/merge/map.json: [{"id": "kebab-slug", "name": "plugin name", "ask": "one sentence", "plugin": "2–3 sentences: how it works, naming the layer mechanics", "layer": "...", "feasibility": "full|workaround", "members": ["{batch}:{index in that batch's array}", ...]}]. Every batch cluster appears in exactly one canonical plugin — check it with a script.

## Step 2 — compute (a script, never estimated)
Union the member clusters' issue numbers (dedupe), then re-sum from corpus.tsv: issues, open, thumbs_up, reactions, comments. demand = thumbs_up + 0.25·(reactions − thumbs_up) + 0.5·comments + issues. Rank by demand. Also list the top 3 issues by reactions per plugin. Write plugin-demands/merge/ranked.json.

## Step 3 — spot-check
For the top 30, read 3 member issues each in corpus.tsv and confirm they truly ask for that plugin; move or drop misfits and recompute. Report how many you moved.

## Step 4 — write the report
.professor/RR/claude-code-plugin-demand-2026-09-27.md:
- Title, date, one-paragraph method (corpus: 57,950 issues = 12,541 open + 45,409 closed not_planned of anthropics/claude-code, harvested 2026-09-26; 26 area batches; the demand formula; filter = solvable or work-around-able by a plugin per plugin-builder's layer table).
- Totals: rows judged, kept, batch clusters, canonical plugins.
- Top 25 in detail: rank, name, demand, issues/open/👍/comments, layer, feasibility, the ask, the plugin sketch, top 3 issues as links https://github.com/anthropics/claude-code/issues/N with their titles.
- Full ranked table of every canonical plugin (rank, name, demand, issues, 👍, layer, feasibility).
- A short section: the function-hooks-only plugins (cannot enter the official directory) and where demand clusters by layer.
No machine-absolute path in the report. Numbers only from ranked.json.

Write only under plugin-demands/merge/ and that one report file. Final reply: one line — `DONE plugins=N top1="name" report=PATH moved=N` or `FAILED: cause`.
