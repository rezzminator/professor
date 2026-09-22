# token-audit

One read-only script over both engines' transcripts, one pricing table: **where did the tokens go?**
Zero dependencies (node: builtins only), no network, nothing written outside `--out` / `--metrics-out` /
a flight's own `metrics.md`.

```bash
node .claude/commands/tokens/token-audit.mjs            # last 24h, every project
node .claude/commands/tokens/token-audit.mjs --since 3d --project <substr>
node .claude/commands/tokens/token-audit.mjs --codex    # Codex CLI threads
node .claude/commands/tokens/token-audit.mjs --flight /tmp/{project}/flights/<name>
```

A RUN is one transcript file: one main chat loop, one sub-agent, or one Codex rollout thread.
A FAMILY is a main chat plus every sub-agent it spawned.

## Flags

| Flag | Purpose |
| --- | --- |
| `--since 24h\|3d` | Window (default `24h`). Cuts per call **and** skips files older than the window. |
| `--root <dir>` | Extra Claude transcript root (repeatable). A root that will not resolve is a hard error. |
| `--project <substr>` | Keep runs whose cwd contains the substring. |
| `--family <substr>` | Drill into one family — matches a chat title, an agent type, a sub-agent's spawn description, or a session-id prefix. |
| `--session <sid-prefix>` | Restrict every section to one session. The selector a sub-agent-orchestrated run has, where a chat title does not exist. |
| `--top <n>` | Rows per section (default 12). |
| `--out <file>` | Full JSON dataset. |
| `--codex` | Read Codex rollouts instead of Claude transcripts. |
| `--codex-root <dir>` | Override `~/.codex`. |
| `--flight <dir>` | Per-flight metrics report (below). |
| `--metrics-out <file>` | Write the flight report here instead of `<dir>/metrics.md`. |
| `--view <file>` / `--briefs <file>` | Compact per-project page data / the briefs-and-behaviour dataset. |

Discovery order for Claude roots when `--root` is absent: `$CLAUDE_CONFIG_DIR/projects`,
`~/.claude/projects`, then every `~/.cc/*/projects`. Roots are resolved through symlinks and
de-duplicated, and each file is de-duplicated by its last three path segments — one project
tree shared by several accounts is never double-counted.

## What it reads

**Claude Code.** `{root}/{projectSlug}/{conversationId}.jsonl` (the main loop) and
`{conversationId}/subagents/agent-{agentId}.jsonl` (+ `.meta.json` for `agentType`,
`description`, `spawnDepth`), including nested `subagents/workflows/wf_*/agent-*.jsonl`.

- Usage rides on every `assistant` line at `message.usage`; the model is `message.model`.
- **Dedup is mandatory**: streaming writes several lines per API call sharing one `message.id`.
  The tool keys on `(message.id, requestId)` and keeps the last — summing raw lines overcounts 2-3x.
- `tool_use` blocks are paired with their `tool_result` so every tool call, its size, its
  duration and its `is_error` are known — that is what makes the per-agent measures possible.
- A `system`/`compact_boundary` line, or a context that shrinks below 60% of what is carried,
  resets the replay: that is a compaction.

**Codex CLI.** `~/.codex/sessions/**/rollout-*.jsonl` and `~/.codex/archived_sessions/**`.
The `~/.codex/` root itself is deliberately not scanned — it holds `rollout-backup-*.jsonl`
copies that would double-count a thread.

- Line 1 is `session_meta`: `id` (thread), `session_id` / `parent_thread_id` (the parent thread),
  `context_window.window_id`, `cwd`, `thread_source`, and `source.subagent` (the role) for a
  sub-agent thread. A Codex sub-agent writes its **own** rollout, so it is attributed individually.
- `turn_context` carries the model; `token_count` carries the counters; `custom_tool_call` /
  `function_call` and their `*_output` carry the tools, their output size and their failures.
- For the `--codex` table only matching lines are decoded (rollouts reach 110 MB); `--flight`
  decodes the few matched rollouts in full.

## Counting and pricing

- **Codex counters reset.** `info.total_token_usage` is cumulative but restarts on resume and on
  compaction, and duplicate events re-emit an identical cumulative. So: dedupe on the cumulative,
  split the thread wherever it drops, sum **each segment's peak**. The final counter alone
  undercounts a long thread by orders of magnitude; summing per-turn deltas double-counts.
  Cached input is a subset of input and bills at the cached rate; output already includes reasoning.
- **Claude cache writes** split 5-minute (1.25x input) from 1-hour (2x input) via
  `cache_creation.ephemeral_1h_input_tokens`; cache reads bill at the per-model rate in `PRICING`
  (0.025x input on Fable/Mythos 5.1, 0.1x elsewhere).
- **Every dollar is traced to the context that caused it**: the replay charges `[0, cache_read)`
  at the read rate, `[cache_read, +cache_write)` at the write rate and the tail at 1x, then
  attributes each slice to the category that put it there.
- `PRICING` at the top of `token-audit.mjs` is an **editable** table, matched by substring on the
  lowercased model id, first match wins — keep specific ids above broader ones. Columns 5 and 6
  are the >200K long-context multipliers, an **estimate** that feeds the CROSS-CHECK line only.
  `scripts/check-token-pricing.mjs` resolves published ids against the table; a row no published
  id reaches is dead code and it says so.

## Honesty rules

- **`data gaps:`** prints on every report: malformed lines, calls with no timestamp, dropped
  synthetic/zero-usage calls, unpriced calls with their models, cache writes with no tier split,
  duplicate files, and read errors. `data gaps: none` means the scan was clean.
- **An unpriced model renders `n/a`, never `$0`.** Its tokens stay in every token total; its
  dollars stay out of every dollar total; the gaps line names the model.
- **A read error is a failure to look, not an empty result.** It is named in the gaps line and the
  process exits non-zero; an unresolvable `--root` is a hard error before anything is reported.
- **`CROSS-CHECK`** compares the estimate against the harness's own `cost-state` line for chats
  wholly inside the window, and prints a second number at the long-context premium. When no chat
  qualifies it says the estimate is UNCHECKED on this host.

## `--flight <dir>`

Writes `<dir>/metrics.md` (or `--metrics-out FILE`) and prints it; `--out FILE` adds JSON.
The window comes from the flight, not from `--since`. Bounded: the text stays under ~200 lines
whatever the flight's size.

One row per agent — task id, agent type, engine, model, calls, wall time, start context, peak
context, growth per call, input / cached / output tokens, price, failed commands, poll calls,
re-reads, contract-file reads (`CLAUDE.md` / `AGENTS.md`), compactions, over cap, matched —
then totals per agent type, the flight total, the three most expensive agents, the gaps line
and the cross-check line.

The join key is `<dir>/agents.tsv`, append-only, tab-separated, one row per spawn, header optional:

```text
task-id	agent-type	agent-id	round	spawn-time(ISO)	engine
1-a	flights-mechanical-executor	a1b2c3	1	2026-09-20T09:01:00Z	claude
2-b	flights-mechanical-executor	01a0…f302	2	2026-09-20T09:40:00Z	codex
```

- `engine` is `claude`, `codex` or `seat` (a seat is tried on both).
- Claude rows resolve to `…/subagents/agent-{agent-id}.jsonl` under any discovered root.
- Codex rows match `{agent-id}` against the rollout's own `session_meta` — `id`,
  `context_window.window_id`, or the id in the filename — never `session_id` /
  `parent_thread_id`, which name the parent on a sub-agent thread.
- A Codex `{agent-id}` starting with `/` is an agent path (`/root/fix_1a`), the only handle a
  Codex orchestrator holds: it matches `session_meta.agent_path`, the spawn nearest the row's
  time winning when a path repeats, and the row is marked `matched: path`.
- A row whose id form cannot be matched falls back to **that row's** spawn time plus its agent
  type, and the row is marked `matched: window`. A spawn time without a clock (`2026-09-20`)
  never opens a window: the row matches by id or path, or is `UNMATCHED`.
- With no `agents.tsv` at all, `run.md`'s header instant and its `{id} CLAIMED · {agent} · {time}`
  lines are the fallback and every row is marked `window`. A header with no parseable instant is
  a hard error — the tool never invents a window.
- A ledger row with no transcript, and a transcript inside the window under this flight's parent
  with no ledger row, are both listed under `UNMATCHED`. Neither is dropped.
- The call cap comes from the agent type name: a `gater` is capped at 150, everything else at 80.

## Tests

```bash
node --test .claude/commands/tokens/
```

`token-audit.test.mjs` runs the CLI as a child process over the synthetic JSONL under
`fixtures/` — a Claude main chat with four sub-agents (a re-read, a failed Bash, a compaction,
85 calls, an unpriced model, a synthetic call, a lone `sleep`), a Codex rollout whose counter
resets after a compaction with three empty `write_stdin` polls, and a flight ledger exercising
an id match, a window match and a row with no transcript. Set `TOKEN_AUDIT_BIN` to point the
suite at another build. No fixture is markdown: every `.md` below the commands tree would
compile into a slash command.
