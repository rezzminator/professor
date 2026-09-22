---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: tokens
description: Attributes runtime token spend, heaviest first — Claude Code chats and sub-agents, Codex CLI threads with `--codex`, one flight's agents with `--flight <dir>`. Flags `--since 24h|3d`, `--project`, `--family`, `--session`, `--top N`, `--out FILE`, `--metrics-out FILE`. Triggers "token audit", "which agent burned the most", "what did the flight cost". Static context size → /context-meter.
---

# Token Audit

One script reads both engines' transcripts against one pricing table. Read-only, no network:

```bash
node ~/.claude/commands/tokens/token-audit.mjs [flags]
```

`~/.claude/commands/tokens/README.md` carries the mechanics, the schema notes and the counting rules.

## Which invocation answers which question

- Where did the last day go: no flags — the default report, bounded, with `data gaps:` and `CROSS-CHECK` lines.
- A longer window, one repo: `--since 3d --project <substr>`.
- Heaviest single runs: section `9 · TOP SINGLE RUNS`; heaviest agent groups: section `8`.
- One chat and its agents: `--family <title|agent-type|session-id-prefix>`, or `--session <sid-prefix>` when a sub-agent orchestrated the work and no chat title exists.
- Codex threads: `--codex` — one row per rollout thread, sub-agents attributed from `session_meta.source`.
- One flight's agents: `--flight <dir>` — see below.
- The full dataset for a page or a diff: `--out FILE` (JSON).

## One flight — `--flight <dir>`

```bash
node ~/.claude/commands/tokens/token-audit.mjs --flight /tmp/{project}/flights/<name>
```

Writes `<dir>/metrics.md` (override with `--metrics-out FILE`; `--out FILE` adds the JSON) and prints the same report. One row per agent: task id, agent type, engine, model, calls, wall time, start and peak context, growth per call, input/cached/output tokens, price, failed commands, poll calls, re-reads, contract-file reads, compactions, over-cap, and how the row was matched. Then totals per agent type, the flight total, the three most expensive agents, the gaps line and the cross-check line. The text stays under ~200 lines whatever the flight's size.

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

- The `data gaps:` line is the report's own honesty: malformed lines, dropped synthetic calls, unpriced calls, cache writes with no 5m/1h split, duplicate files, read errors. `data gaps: none` means the scan was clean, not that nothing was checked. A read error exits non-zero.
- A model with no `PRICING` row renders **`n/a`**, never `$0`: its tokens stay in every token total, its dollars stay out of every dollar total, and the gaps line names it.
- Costs are list-price estimates from the editable `PRICING` table atop `token-audit.mjs`. Trust the ranking; verify absolute dollars against the provider's billing; update the rates when prices change. `scripts/check-token-pricing.mjs` resolves published model ids against that table.
- `CROSS-CHECK` compares the estimate to the harness's own `cost-state` line for chats wholly inside the window, and prints a second number at the >200K long-context premium (a per-model rate in `PRICING`, and an estimate).
- Codex counts differently: `total_token_usage` is cumulative and **resets on resume and compaction**, so each segment's peak is summed. Cached input is a subset of input, billed at the cached rate; output already includes reasoning.
- Transcript content can carry sensitive prompt text — read the report, never pipe or retain transcript bodies.
