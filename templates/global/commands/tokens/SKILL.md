---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: tokens
description: Attributes runtime token spend, heaviest first — Claude Code sub-agents and Workflow runs, or Codex CLI threads with `--codex`. Flags `--all`, `--by-workflow`, `--filter <substr>`, `--detail <id>`, `--by-day`, `--since <date>`, `--top N`, `--session <id>`; `--help` lists all. Triggers "token ledger", "which agent burned the most", "what did the wave cost". Static context size → /context-meter.
---

# Token Ledger

Run from the repo root (the project slug derives from cwd); read-only over transcripts, no network:

```bash
node ~/.claude/commands/tokens/token-ledger.mjs [flags]
```

`--help` prints the full flag list; `~/.claude/commands/tokens/README.md` carries the mechanics and schema notes.

## Which invocation answers which question

- Heaviest burner: `--all` — the per-agent table sorts by est cost descending, so the top row is the answer.
- One Workflow run's cost: `--all --by-workflow`, the run's `wf_*` row.
- One wave, pipeline, or feature: `--all --filter <label>` — sums every agent row whose label carries it.
- A whole chat's spend: `--by-workflow` in that chat (default scope) — `TOTAL` is the chat, `wf_*` rows are its Workflow runs, `(non-workflow agents)` is everything else.
- One agent's individual calls: `--detail <id|label-substr>`.

## What gets a `wf_*` row

Only a Workflow-engine run — a script under the repo's `workflows/` or a skill-embedded engine (`/deep-rr`). An orchestrated wave is not one: `/wave:orchestrator` and `/wave:builder` run in their chats' main sessions and spawn session-level sub-agents, which land in `(non-workflow agents)`; total a wave with `--filter <wave-label>` instead. `/wave:walker` spawns session-level agents too, so it lands under the same filter. A dual-chat wave spans two chats — sum default scope in the orchestrator chat with `--session {builder-session}` for the builder.

## Codex sessions — `--codex`

Same script, same PRICING table, reading `~/.codex/sessions/**/rollout-*.jsonl` plus `archived_sessions/`. One row per session thread; because a Codex subagent writes its own rollout, subagents are attributed individually by their agent role.

```bash
node ~/.claude/commands/tokens/token-ledger.mjs --codex --since <YYYY-MM-DD>            # this repo, per session
node ~/.claude/commands/tokens/token-ledger.mjs --codex --since <YYYY-MM-DD> --by-day   # daily spend
node ~/.claude/commands/tokens/token-ledger.mjs --codex --all --top 0                   # every project, every row
```

Scope defaults to the repo you are standing in; `--all` spans every project and adds a PROJECT column. `--since` reads the rollout filename stamp (local time). The session table caps at 25 rows — `--top 0` prints all.

Codex counting differs from Claude's per-call dedup: `total_token_usage` is cumulative but **resets on resume/compaction**, so the tool sums each segment's peak. Reading only the final counter undercounts a long session by orders of magnitude, and summing the per-turn deltas overcounts (duplicate events re-emit an identical cumulative). Cached input is a subset of input, billed at the cached rate; output already includes reasoning.

## Reading the output

- Claude footer totals: output-only, in+out, fresh (in+out+cache-write), grand total (+cache-read). The harness's `subagent_tokens` is the fresh number — it excludes cache-read, usually the largest component of real spend.
- Costs are estimates from the editable `PRICING` table atop `token-ledger.mjs`: trust the ranking, verify absolute dollars against the provider's billing, update the rates when prices change. A model with no PRICING row reports cost `n/a` with its tokens still counted — never a silent $0.
- `--detail` content hints can carry sensitive prompt text — read it, never pipe or retain it.
