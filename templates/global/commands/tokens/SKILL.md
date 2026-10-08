---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: tokens
description: Ranks runtime spend — token audits of Claude chats, Codex threads (--codex), or a flight (--flight DIR). Flags --since 24h|3d, --project, --family, --session, --top N, --out FILE, --metrics-out FILE, --timeline FILE, --view FILE, --briefs FILE. Returns a spend report. Current price details → pfm model-cost; static context → /context-meter.
---

# Token Audit

The script reads both engines' transcripts; pfm retrieves official live pricing before the scan:

```bash
node ~/.claude/commands/tokens/token-audit.mjs [flags]
```

`~/.claude/commands/tokens/README.md` carries the mechanics, the schema notes and the counting rules.

## Which invocation answers which question

- Where did the last day go: no flags — the default report, bounded, with `data gaps:` and `CROSS-CHECK` lines.
- A longer window, one repo: `--since 3d --project <substr>`.
- Heaviest single runs: section `9 · TOP SINGLE RUNS`; heaviest agent groups: section `8`; both carry each row's cache TTL (legend below, under `--flight`).
- One chat and its agents: `--family <title|agent-type|session-id-prefix>`, or `--session <sid-prefix>` when a sub-agent orchestrated the work and no chat title exists.
- Codex threads: `--codex` — one row per rollout thread, sub-agents attributed from `session_meta.source`.
- One flight's agents: `--flight <dir>` — see below.
- One agent run call by call: `--timeline <transcript.jsonl>` (repeatable) — whole file, no window; a header (agent type, models, effort, calls, wall, peak context, output, tool errors, results over 20 KB, USD) then one row per model call with its clock, wait since the last tool result, context, output, price and each tool it issued (`name: target`, result chars, `ERR`, wait).
- The full dataset for a page or a diff: `--out FILE` (JSON).

## One flight — `--flight <dir>`

```bash
node ~/.claude/commands/tokens/token-audit.mjs --flight $HOME/.local/state/pfm/flights/{project}/<name>
```

Writes `<dir>/metrics.md` (override with `--metrics-out FILE`; `--out FILE` adds the JSON) and prints the same report. One row per agent: task id, agent type, engine, model, calls, wall time, start and peak context, growth per call, input/cached/output tokens, cache TTL (`5m`, `1h`, `1h N%` when mixed, `—` for no cache writes or an engine that reports no split, as Codex), price, failed commands, poll calls, re-reads, contract-file reads, compactions, over-cap, and how the row was matched. Then totals per agent type, the flight total, the three most expensive agents, the gaps line and the cross-check line. The text stays under ~200 lines whatever the flight's size.

The join key is `<dir>/agents.tsv` — append-only, tab-separated, one row per spawn, header line optional:

```text
task-id	agent-type	agent-id	round	spawn-time(ISO)	engine
1-a	flights-mechanical-executor	a1b2c3	1	2026-09-20T09:01:00Z	claude
```

- Claude rows match `…/subagents/agent-{agent-id}.jsonl` under any discovered root.
- Codex rows match `{agent-id}` against the rollout's own `session_meta` (`id`, `context_window.window_id`, or the id in the filename); an id starting with `/` is an agent path and matches `session_meta.agent_path`, marked `matched: path`.
- A row whose id form cannot be matched falls back to **that row's** spawn time plus its agent type and is marked `matched: window`; a spawn time without a clock never opens a window.
- With no `agents.tsv` at all, `run.md`'s header instant and its `{id} CLAIMED · {agent} · {time}` lines are the fallback and **every** row is marked `window`.
- A ledger row with no transcript and a transcript inside the window with no ledger row are both listed under `UNMATCHED` — never dropped; each unmatched transcript carries its price, and the `unledgered` line gives their sum and the flight's whole spend.

## Reading the output

- The `data gaps:` line is the report's own honesty: malformed lines, dropped synthetic calls, unpriced calls, cache writes with no 5m/1h split, calls copied from another transcript, duplicate files, read errors. `data gaps: none` means the scan was clean, not that nothing was checked. A read error exits non-zero.
- A model id that resolves to no priced key, or a missing rate for a used token dimension, renders **`n/a`**, never `$0`; its tokens still count and the gaps line names it. A dated snapshot id resolves to its undated key; no family or substring match exists.
- Prices come from `pfm model-cost --json --all`: pfm's one table, refreshed from both official provider pages at most once a day (`--force` refreshes now, `PFM_PRICES_OFFLINE=1` never fetches); a failed refresh is named in the gaps line. Estimates use Standard token/cache prices and each row's long-context tier. `TOKEN_AUDIT_PRICES` selects a saved `--json --all` document or pfm's `prices.json`; the report names the table's fetch time. Current-price estimates are not historical bills.
- `CROSS-CHECK` compares published context-tier estimates to the harness's `cost-state` line for chats wholly inside the window.
- Codex counts differently: `total_token_usage` is cumulative and **resets on resume and compaction**, so each segment's peak is summed. Cached input is a subset of input; output includes reasoning. Aggregate dollars use the run's peak-context tier, an approximation.
- Transcript content can carry sensitive prompt text — read the report, never pipe or retain transcript bodies.
